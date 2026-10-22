package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/website"
)

// These PUBLIC, throwaway fixture credentials have no value outside this mode.
// Never create these records in ordinary init/serve.
const localWebsiteIdentity = "v1_0000000000000000000000000000000000000000000000000000000000000000"
const localWebsiteKey = "local-demo-key"
const localWebsiteCollection = ledger.AccountCollectionName

func websiteLocalFailure(stage, constraint string) *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RuntimeSetupUnknown)
	d.Code = "SITE_LOCAL_UNAVAILABLE"
	d.Stage = stage
	d.Constraint = constraint
	d.Outcome = "local_fixture_not_ready"
	d.NextStep = "Local website setup cause unknown. Inspect the fixed code/stage privately; no production state was opened. Do not use real credentials."
	return d
}

// website-local is an explicitly disposable, loopback-only mode of this binary.
// It ignores real runtime keys/credentials, constructs no telemetry, accepts no
// state path, and uses the existing HTTP/maintenance drain-and-join coordinator.
func executeWebsiteLocal(ctx context.Context, args []string, observer failureowner.Observer) (result error) {
	owned := false
	stage := "site_local_options"
	defer func() {
		if recover() != nil {
			d := websiteLocalFailure(stage, "completed_local_fixture_lifecycle")
			d.Outcome = "local_fixture_state_unknown"
			failureowner.Observe(observer, d.Kind())
			if result == nil {
				result = d
			}
			owned = true
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
		if drain, ok := result.(*unconfirmedHTTPDrain); ok {
			result = drain.Diagnostic
		}
	}()
	flags := flag.NewFlagSet("website-local", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := flags.String("listen", "127.0.0.1:8443", "Numeric loopback listener")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return runtimeFailure(codeSyntheticOptions, stage, "website_local_listen_only", "no_effect", "Use website-local with optional --listen and a numeric loopback address. State, credentials and TLS keys are disposable synthetic fixtures only.")
	}
	if err := validateListen(*address); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	stage = "site_local_scratch"
	scratch, err := os.MkdirTemp("", "possums-intake-website-local-")
	if err != nil {
		return websiteLocalFailure(stage, "private_temporary_state")
	}
	removeScratch := false
	defer func() {
		if e := cleanupScratch(scratch, removeScratch); e != nil {
			failureowner.Observe(observer, ledger.FailureKind(e, failuretype.RuntimeShutdownUnknown))
			if result == nil {
				result = e
				owned = true
			}
		}
	}()
	stage = "site_local_init"
	if err := ledger.Init(ctx, scratch); err != nil {
		return err
	}
	stage = "site_local_open"
	service, err := ledger.Open(ctx, scratch)
	if err != nil {
		return err
	}
	defer func() {
		panicked := recover() != nil
		closeErr := service.CloseObserved(observer)
		if panicked {
			d := websiteLocalFailure(stage, "completed_local_fixture_lifecycle")
			d.Outcome = "local_fixture_state_unknown"
			failureowner.Observe(observer, d.Kind())
			if result == nil {
				result = d
			}
			owned = true
		}
		removeScratch = !panicked && closeErr == nil && !httpDrainUnconfirmed(result) && (result == nil || ledger.Failure(result).Code != codeRuntime)
		if result == nil && closeErr != nil {
			result = closeErr
			owned = true
		}
	}()
	stage = "site_local_customer_fixture"
	app, err := service.CustomerAuthApp()
	if err != nil {
		return err
	}
	if err = seedWebsiteCustomer(ctx, app); err != nil {
		return err
	}
	auth, err := siteauth.New(app, localWebsiteCollection)
	if err != nil {
		return err
	}
	stage = "site_local_prepare"
	bundle, err := website.Bundled()
	if err != nil {
		return err
	}
	revision, err := website.Prepare(ctx, bundle)
	if err != nil {
		return err
	}
	// Random private accounting key is never presented or copied into a form.
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return websiteLocalFailure(stage, "private_accounting_key")
	}
	fallback, err := httpapi.NewHandler(service, func() bool { return ctx.Err() == nil && service.Ready() }, hex.EncodeToString(key), nil)
	if err != nil {
		return websiteLocalFailure(stage, "existing_accounting_fallback")
	}
	handler, err := website.NewFixture(revision, siteauth.FixtureKeyAdapter{Adapter: auth, Identity: localWebsiteIdentity}, fallback, website.Config{Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16, FailureObserver: observer})
	if err != nil {
		return err
	}
	stage = "site_local_tls"
	tlsConfig, err := localWebsiteTLS()
	if err != nil {
		return err
	}
	stage = "site_local_bind"
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return runtimeFailure(codeBind, stage, "available_loopback_listener", "no_effect", "Local HTTPS listener unavailable; cause unknown. Check that the chosen loopback port is free. No production state was opened.")
	}
	defer func() {
		if e := listener.Close(); e != nil && !errors.Is(e, net.ErrClosed) {
			d := runtimeFailure(codeShutdown, "site_local_listener_close", "closed_local_listener", "unknown", "Listener cleanup cause unknown. Preserve temporary state if teardown is unconfirmed; do not assume delivery or retry an action.")
			failureowner.Observe(observer, d.Kind())
			if result == nil {
				result = d
				owned = true
			}
		}
	}()
	stage = "site_local_serve"
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	server := localServer(work, handler)
	server.TLSConfig = tlsConfig
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	owned = true
	return coordinateServeObserved(ctx, work, cancel, service, server, tls.NewListener(listener, tlsConfig), ticker.C, time.Now(), observer)
}

func seedWebsiteCustomer(ctx context.Context, app core.App) (result error) {
	defer func() {
		if recover() != nil {
			result = websiteSeedFailure(nil)
		}
	}()
	err := app.RunInTransaction(func(tx core.App) error {
		c, err := tx.FindCollectionByNameOrId(localWebsiteCollection)
		if err != nil {
			return err
		}
		record := core.NewRecord(c)
		record.Set("key_lookup", localWebsiteIdentity)
		record.Set("balance", "0")
		record.Set("enabled", true)
		record.SetPassword(localWebsiteKey)
		return tx.SaveWithContext(ctx, record)
	})
	if err != nil {
		return websiteSeedFailure(err)
	}
	return nil
}

func websiteSeedFailure(cause error) *ledger.Diagnostic {
	if cause == nil {
		cause = &ledger.Diagnostic{}
	}
	d := ledger.CanonicalFailure(cause, failuretype.NativeWriteUnknown)
	safe := websiteLocalFailure("site_local_customer_fixture", "atomic_synthetic_auth_fixture")
	d.Code, d.Stage, d.Constraint, d.Outcome, d.NextStep = safe.Code, safe.Stage, safe.Constraint, safe.Outcome, safe.NextStep
	if d.Kind() == failuretype.Cancelled || d.Kind() == failuretype.DeadlineExceeded {
		d.Code = "SITE_LOCAL_INTERRUPTED"
		d.NextStep = "Setup was interrupted. No website listener started; restart the disposable local mode if desired."
	}
	return d
}

// An ephemeral self-signed localhost certificate: no file, CA install, proxy
// trust, public binding or remote resource is needed for the manual walkthrough.
// Automated clients explicitly trust its certificate, never InsecureSkipVerify.
func localWebsiteTLS() (*tls.Config, error) {
	fail := func() (*tls.Config, error) {
		return nil, websiteLocalFailure("site_local_tls", "ephemeral_loopback_certificate")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fail()
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fail()
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Possums disposable local website"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fail()
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, nil
}
