package main

import (
	"context"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/internal/siteauth"
	"possums-intake/internal/website"
)

func websiteSetupOptionsFailure() *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, failuretype.RequestInvalid)
	d.Code, d.Stage, d.Constraint, d.Outcome = "SITE_INITIALIZATION_OPTIONS_INVALID", "website_initialize", "explicit_state_optional_existing_volume_only", "no_effect"
	d.NextStep = "Use website-init --state with an absolute initialized ledger directory and optionally --tinfoil-volume for existing storage. Never supply initialization, listener or operator flags. This adds website/auth schema only; it never initializes the ledger or creates or changes login credentials."
	return d
}

// Explicit setup only: serving never initializes schema, credentials or code.
func initializeWebsite(ctx context.Context, app core.App) error {
	if err := siteauth.InitializeCustomerAuth(app); err != nil {
		return err
	}
	return website.InitializeDefaultPublication(ctx, app)
}

func initializeWebsiteStateObserved(ctx context.Context, dir string, observer failureowner.Observer) (result error) {
	owned := false
	defer func() {
		if recover() != nil {
			result = runtimeFailure("SITE_INITIALIZATION_UNAVAILABLE", "website_initialize", "explicit_setup_completion_cause_unknown", "website_setup_unconfirmed", "Preserve partial state and inspect privately. No credentials or credits were seeded; do not reset financial state.")
			owned = false
		}
		if result != nil && !owned {
			failureowner.Observe(observer, ledger.FailureKind(result, failuretype.RuntimeSetupUnknown))
		}
	}()
	service, err := ledger.OpenObserved(ctx, dir, observer)
	if err != nil {
		owned = true
		return err
	}
	defer func() {
		if err := service.CloseObserved(observer); err != nil && result == nil {
			result, owned = err, true
		}
	}()
	app, err := service.CustomerAuthApp()
	if err != nil {
		return err
	}
	return initializeWebsite(ctx, app)
}

func newWebsiteHandler(service *ledger.Service, fallback http.Handler, observer failureowner.Observer) (*website.Host, error) {
	app, err := service.CustomerAuthApp()
	if err != nil {
		return nil, err
	}
	auth, err := siteauth.NewCustomer(app)
	if err != nil {
		return nil, err
	}
	// The operator selected Tinfoil's HTTPS ingress as the transport boundary.
	// This is deployment policy, not evidence of direct Go TLS or trust in a
	// Forwarded/X-Forwarded-Proto header. Never expose this listener directly.
	host, err := website.New(auth, fallback, website.Config{
		Lifetime: time.Hour, IdleTimeout: 10 * time.Minute, ConcurrentRequests: 16,
		FailureObserver: observer, Balances: service, TLSOffloaded: true,
	})
	if err != nil {
		return nil, err
	}
	if err := host.BindPublication(app); err != nil {
		// Bind leaves website admission closed. Keep native administration and
		// accounting available for explicit code repair; never select a fallback.
		failureowner.Observe(observer, ledger.FailureKind(err, failuretype.NativeReadUnknown))
	}
	return host, nil
}
