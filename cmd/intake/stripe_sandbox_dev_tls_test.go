package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func sandboxDevTLSFixture(t *testing.T, config *tls.Config, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = config
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func sandboxDevTestClient(t *testing.T, der []byte, server *httptest.Server) *http.Client {
	t.Helper()
	client, err := sandboxDevReadinessClient(der)
	if err != nil {
		t.Fatal("DEV_TEST_PROBE_CERTIFICATE")
	}
	transport := client.Transport.(*http.Transport)
	// Only the test dial target differs. HTTP request, TLS hostname, trusted
	// root and exact leaf verification are the actual fixed production helper.
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "127.0.0.1:8443" {
			return nil, sandboxDevFailure("stripe_dev_probe", "authored_fixed_destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(client.CloseIdleConnections)
	if transport.Proxy != nil || transport.TLSClientConfig.InsecureSkipVerify || !transport.DisableKeepAlives || !transport.DisableCompression || client.Timeout <= 0 {
		t.Fatal("DEV_TEST_PROBE_TRANSPORT_POLICY")
	}
	return client
}

func TestSandboxDevProbeExactCertificateStatusAndBody(t *testing.T) {
	for _, mode := range []string{"valid", "untrusted", "hostname", "redirect", "status", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			config, err := localWebsiteTLS()
			if err != nil {
				t.Fatal("DEV_TEST_RUNTIME_CERTIFICATE")
			}
			der := config.Certificates[0].Certificate[0]
			if mode == "untrusted" {
				other, err := localWebsiteTLS()
				if err != nil {
					t.Fatal("DEV_TEST_OTHER_CERTIFICATE")
				}
				der = other.Certificates[0].Certificate[0]
			}
			if mode == "hostname" {
				cert, _ := x509.ParseCertificate(der)
				cert.IPAddresses = nil
				cert.DNSNames = []string{"wrong.example.invalid"}
				key := config.Certificates[0].PrivateKey
				der, err = x509.CreateCertificate(rand.Reader, cert, cert, key.(crypto.Signer).Public(), key)
				if err != nil {
					t.Fatal("DEV_TEST_HOSTNAME_CERTIFICATE")
				}
				config.Certificates[0].Certificate[0] = der
			}
			var calls atomic.Int64
			server := sandboxDevTLSFixture(t, config, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.RequestURI() != "/login" || r.Host != "127.0.0.1:8443" || r.Header.Get("Authorization") != "" || len(r.Cookies()) != 0 {
					t.Error("DEV_TEST_PROBE_SCOPE")
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "/must-not-follow")
					w.WriteHeader(http.StatusFound)
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "oversize":
					_, _ = io.WriteString(w, strings.Repeat("SYNTHETIC_PRIVATE_SENTINEL", 4096))
				default:
					_, _ = io.WriteString(w, "synthetic login")
				}
			}))
			client := sandboxDevTestClient(t, der, server)
			err = sandboxDevProbeWith(context.Background(), client)
			if (err == nil) != (mode == "valid") {
				t.Fatal("DEV_TEST_PROBE_ACCEPTANCE")
			}
			wantCalls := int64(1)
			if mode == "untrusted" || mode == "hostname" {
				wantCalls = 0
			}
			if calls.Load() != wantCalls {
				t.Fatal("DEV_TEST_PROBE_RETRY_REDIRECT_OR_TLS_BYPASS")
			}
			if err != nil {
				assertClosedRuntime(t, err)
			}
		})
	}
}

func TestSandboxDevActualRuntimeCertificateAfterBothBinds(t *testing.T) {
	h := newSandboxDevHarness(t, "ready")
	website, webhook := syntheticAddress(t), syntheticAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probed := make(chan struct{})
	h.dependencies.app = func(ctx context.Context, key, secret string, bound func([]byte)) error {
		return executeStripeSandboxLocalReady(ctx, []string{"--listen", website, "--webhook-listen", webhook}, nil,
			sandboxLocalTestInput(key, secret), func() bool { return false }, func(der []byte) {
				for _, address := range []string{website, webhook} {
					listener, err := net.Listen("tcp", address)
					if err == nil {
						listener.Close()
						t.Error("DEV_TEST_CALLBACK_BEFORE_BOTH_BINDS")
					}
				}
				bound(der)
			})
	}
	h.dependencies.probe = func(ctx context.Context, der []byte) error {
		client, err := sandboxDevReadinessClient(der)
		if err != nil {
			return err
		}
		defer client.CloseIdleConnections()
		client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, website)
		}
		err = sandboxDevProbeWith(ctx, client)
		if err == nil {
			close(probed)
		}
		return err
	}
	done := make(chan error, 1)
	go func() { done <- executeStripeSandboxDevWith(ctx, nil, nil, h.dependencies) }()
	sandboxAwait(t, probed)
	cancel()
	if sandboxDevResult(t, done) != nil {
		t.Fatal("DEV_TEST_ACTUAL_RUNTIME_JOIN")
	}
	for _, address := range []string{website, webhook} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal("DEV_TEST_ACTUAL_LISTENER_NOT_JOINED")
		}
		listener.Close()
	}
}
