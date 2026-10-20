package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func TestWebsiteLocalOptionsAreIsolated(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	for _, args := range [][]string{{"--state=/SYNTHETIC_PRIVATE_SENTINEL"}, {"--operator"}, {"--listen=0.0.0.0:8443"}, {"--listen=192.0.2.1:8443"}, {"--listen=localhost:8443"}, {"--listen=127.0.0.1:08443"}, {"unexpected"}} {
		err := executeContext(context.Background(), append([]string{"website-local"}, args...))
		if err == nil {
			t.Fatal("SITE_TEST_UNSAFE_OPTIONS_ACCEPTED")
		}
		d := ledger.Failure(err)
		if d.Stage == "" || d.Constraint == "" || d.NextStep == "" || strings.Contains(d.Error(), "SYNTHETIC_PRIVATE_SENTINEL") {
			t.Fatal("SITE_TEST_OPTION_DIAGNOSTIC")
		}
		entries, e := os.ReadDir(tmp)
		if e != nil || len(entries) != 0 {
			t.Fatal("SITE_TEST_OPTIONS_TOUCHED_STATE")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if executeContext(ctx, []string{"website-local"}) != nil {
		t.Fatal("SITE_TEST_NORMAL_CANCEL")
	}
}
func TestWebsiteLocalBindFailureDisposesOnlyScratchAndCountsOnce(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("SITE_TEST_OCCUPIED_LISTENER")
	}
	defer listener.Close()
	observer := &runtimeObserver{}
	err = executeObserved(context.Background(), []string{"website-local", "--listen", listener.Addr().String()}, observer)
	if err == nil || ledger.Failure(err).Code != codeBind {
		t.Fatal("SITE_TEST_BIND_CLASSIFICATION")
	}
	observer.want(t, failuretype.RuntimeServeUnknown)
	entries, e := os.ReadDir(tmp)
	if e != nil || len(entries) != 0 {
		t.Fatal("SITE_TEST_FAILED_BIND_DISPOSAL")
	}
}

func TestLocalWebsiteCertificateIsEphemeralAndLoopback(t *testing.T) {
	first, err := localWebsiteTLS()
	if err != nil {
		t.Fatal("SITE_TEST_TLS_CONFIG")
	}
	second, err := localWebsiteTLS()
	if err != nil {
		t.Fatal("SITE_TEST_TLS_CONFIG")
	}
	if first.MinVersion < tls.VersionTLS12 || first.InsecureSkipVerify || bytes.Equal(first.Certificates[0].Certificate[0], second.Certificates[0].Certificate[0]) {
		t.Fatal("SITE_TEST_TLS_ISOLATION")
	}
	cert, err := x509.ParseCertificate(first.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal("SITE_TEST_TLS_CERTIFICATE")
	}
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		if cert.VerifyHostname(host) != nil {
			t.Fatal("SITE_TEST_TLS_LOCAL_SAN")
		}
	}
	if cert.VerifyHostname("example.invalid") == nil || cert.IsCA {
		t.Fatal("SITE_TEST_TLS_SCOPE")
	}
}

func websiteHidden(t *testing.T, markup, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindStringSubmatch(markup)
	if len(m) != 2 {
		t.Fatal("SITE_TEST_NATIVE_FORM_FIELD")
	}
	return html.UnescapeString(m[1])
}
func websiteRequest(t *testing.T, client *http.Client, base, method, path string, form url.Values) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatal("SITE_TEST_REQUEST")
	}
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", base)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal("SITE_TEST_VERIFIED_TLS_REQUEST")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	response.Body.Close()
	if err != nil {
		t.Fatal("SITE_TEST_READ_RESPONSE")
	}
	for _, s := range []string{localWebsiteIdentity, localWebsiteKey, "SYNTHETIC_PRIVATE_SENTINEL", "goroutine "} {
		if bytes.Contains(data, []byte(s)) {
			t.Fatal("SITE_TEST_PRIVATE_VALUE_IN_RESPONSE")
		}
	}
	return response, string(data)
}

func TestWebsiteLocalCompiledCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "intake")
	if exec.Command("go", "build", "-o", binary, ".").Run() != nil {
		t.Fatal("SITE_TEST_COMPILED_BUILD")
	}
	run := func(t *testing.T) {
		t.Helper()
		tmp := t.TempDir()
		address := syntheticAddress(t)
		p := &process{done: make(chan error, 1), base: "https://" + address}
		p.cmd = exec.Command(binary, "website-local", "--listen", address)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "TMPDIR=") && !strings.HasPrefix(entry, "LEDGER_") && !strings.HasPrefix(entry, "OTEL_") && !strings.HasPrefix(entry, "INTAKE_METRICS_") {
				p.cmd.Env = append(p.cmd.Env, entry)
			}
		}
		// The isolated local mode must not instantiate telemetry or consume runtime
		// admin/operator credentials even when hostile settings are inherited.
		p.cmd.Env = append(p.cmd.Env, "TMPDIR="+tmp, "LEDGER_ADMIN_KEY=SYNTHETIC_PRIVATE_SENTINEL", "LEDGER_OPERATOR_PASSWORD=SYNTHETIC_PRIVATE_SENTINEL", "OTEL_SDK_DISABLED=false", "OTEL_EXPORTER_OTLP_ENDPOINT=SYNTHETIC_PRIVATE_SENTINEL", "INTAKE_METRICS_HONEYCOMB_API_KEY=SYNTHETIC_PRIVATE_SENTINEL")
		p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.stderr
		if p.cmd.Start() != nil {
			t.Fatal("SITE_TEST_PROCESS_START")
		}
		go func() { p.done <- p.cmd.Wait() }()
		t.Cleanup(func() { stopProcess(t, p) })
		client := &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		var cert *x509.Certificate
		// Acquisition sends NO credentials: take the expected process's public
		// self-signed certificate from a correctly rejected trust handshake, then
		// pin it for all actual HTTP requests. Never disable TLS verification.
		deadline := time.NewTimer(12 * time.Second)
		defer deadline.Stop()
		ticks := time.NewTicker(25 * time.Millisecond)
		defer ticks.Stop()
	waiting:
		for {
			select {
			case <-p.done:
				p.stopped = true
				t.Fatal("SITE_TEST_EARLY_EXIT_OUTPUT_WITHHELD")
			case <-deadline.C:
				t.Fatal("SITE_TEST_TLS_READINESS_TIMEOUT")
			case <-ticks.C:
				r, e := client.Get(p.base + "/healthz")
				if r != nil {
					r.Body.Close()
				}
				var unknown x509.UnknownAuthorityError
				if errors.As(e, &unknown) {
					cert = unknown.Cert
					break waiting
				}
			}
		}
		if cert == nil || cert.VerifyHostname("127.0.0.1") != nil {
			t.Fatal("SITE_TEST_PUBLIC_CERTIFICATE_SCOPE")
		}
		pool := x509.NewCertPool()
		pool.AddCert(cert)
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
		defer transport.CloseIdleConnections()
		client.Transport = transport
		client.Jar, _ = cookiejar.New(nil)
		r, _ := websiteRequest(t, client, p.base, "GET", "/healthz", nil)
		if r.StatusCode != 200 || len(r.Cookies()) != 0 {
			t.Fatal("SITE_TEST_RESERVED_HEALTH")
		}
		r, _ = websiteRequest(t, client, p.base, "GET", "/", nil)
		if r.StatusCode != 200 {
			t.Fatal("SITE_TEST_PUBLIC_HOME")
		}
		r, markup := websiteRequest(t, client, p.base, "GET", "/login", nil)
		if r.StatusCode != 200 {
			t.Fatal("SITE_TEST_LOGIN_FORM")
		}
		for _, legacy := range []string{`name="identity"`, `name="password"`} {
			if strings.Contains(markup, legacy) {
				t.Fatal("SITE_TEST_LEGACY_CREDENTIAL_FIELD")
			}
		}
		if !strings.Contains(markup, `name="key"`) {
			t.Fatal("SITE_TEST_SINGLE_KEY_FIELD")
		}
		// One-key demo sign-in: only the pasted key is submitted. The fixed
		// synthetic identity is a private backend selector, never a form field.
		form := url.Values{"key": {localWebsiteKey}, "csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}}
		r, _ = websiteRequest(t, client, p.base, "POST", "/login", form)
		if r.StatusCode != 303 || r.Header.Get("Location") != "/home" {
			t.Fatal("SITE_TEST_FINAL_SDK_LOGIN")
		}
		r, markup = websiteRequest(t, client, p.base, "GET", "/home", nil)
		if r.StatusCode != 200 || !strings.Contains(markup, "You are signed in.") {
			t.Fatal("SITE_TEST_SIGNED_HOME")
		}
		form = url.Values{"csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}}
		r, _ = websiteRequest(t, client, p.base, "POST", "/logout", form)
		if r.StatusCode != 303 || r.Header.Get("Location") != "/" {
			t.Fatal("SITE_TEST_POST_LOGOUT")
		}
		r, _ = websiteRequest(t, client, p.base, "GET", "/home", nil)
		if r.StatusCode != 303 {
			t.Fatal("SITE_TEST_ANONYMOUS_AFTER_LOGOUT")
		}
		entries, e := os.ReadDir(tmp)
		if e != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "possums-intake-website-local-") {
			t.Fatal("SITE_TEST_PRIVATE_SCRATCH")
		}
		scratch := filepath.Join(tmp, entries[0].Name())
		for _, a := range []struct{ file, query string }{{"auxiliary.db", "SELECT count(*) FROM _logs"}, {"data.db", "SELECT count(*) FROM _authOrigins"}} {
			db, e := sql.Open("sqlite", filepath.Join(scratch, a.file)+"?mode=ro")
			if e != nil {
				t.Fatal("SITE_TEST_QUIET_DATABASE")
			}
			var count int
			e = db.QueryRow(a.query).Scan(&count)
			closeErr := db.Close()
			if e != nil || closeErr != nil || count != 0 {
				t.Fatal("SITE_TEST_ACTIVITY_PERSISTED")
			}
		}
		stopProcess(t, p)
		entries, e = os.ReadDir(tmp)
		if e != nil || len(entries) != 0 {
			t.Fatal("SITE_TEST_JOINED_DISPOSAL")
		}
	}
	t.Run("FirstProcess", run)
	t.Run("FreshRestart", run)
}
