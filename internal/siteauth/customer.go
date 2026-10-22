package siteauth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// InitializeCustomerAuth validates the already initialized merged collection.
// Explicit website setup never creates a second auth table, repairs schema or
// changes credentials or money. Ordinary serving does not call this function.
func InitializeCustomerAuth(app core.App) (result error) {
	defer func() {
		if recover() != nil {
			result = customerSchemaFault()
		}
	}()
	if app == nil || app.IsTransactional() {
		return customerSchemaRefused()
	}
	c, err := app.FindCollectionByNameOrId(CustomerCollection)
	if errors.Is(err, sql.ErrNoRows) {
		return customerSchemaRefused()
	}
	if err != nil {
		return classified(customerSchemaFault(), err)
	}
	if !customerSchema(c) {
		return customerSchemaRefused()
	}
	return nil
}

func customerSchema(c *core.Collection) bool { return ledger.AccountCollectionCompatible(c) }

func customerSchemaRefused() *ledger.Diagnostic {
	d := refused("SITE_CUSTOMER_SCHEMA_REFUSED", "customer_auth_schema", "explicit_compatible_customer_schema", failuretype.PolicyRefused)
	d.Outcome = "no_effect"
	d.NextStep = "Use explicit fresh-state initialization outside a transaction; inspect incompatible or colliding schema privately. Do not reset credentials or financial state."
	return d
}

func customerSchemaFault() *ledger.Diagnostic {
	d := refused("SITE_CUSTOMER_SCHEMA_UNAVAILABLE", "customer_auth_schema", "fixed_auth_schema_cause_unknown", failuretype.NativeReadUnknown)
	d.Outcome = "customer_auth_schema_unconfirmed"
	d.NextStep = "Inspect the merged account schema privately; never reset accounts or restore financial state to recover website auth."
	return d
}

// CustomerKeyAdapter converts only the approved key protocol into private final
// SDK authentication. The authenticated record ID is the sole wallet identity.
type CustomerKeyAdapter struct{ sdk *Adapter }

func NewCustomer(app core.App) (adapter *CustomerKeyAdapter, result error) {
	defer func() {
		if recover() != nil {
			adapter = nil
			result = unknown("site_auth_setup")
		}
	}()
	a, err := New(app, CustomerCollection)
	if err != nil {
		return nil, err
	}
	a.customer = true
	if _, err := a.config(); err != nil {
		return nil, err
	}
	return &CustomerKeyAdapter{sdk: a}, nil
}

func (a *CustomerKeyAdapter) Login(ctx context.Context, key string) (string, Principal, error) {
	material, err := deriveKey(key)
	if err != nil {
		return "", Principal{}, refused("SITE_AUTH_INPUT", "site_auth_login", "gateway_key_text", failuretype.RequestInvalid)
	}
	return a.sdk.Login(ctx, material.lookup, material.password)
}

func (a *CustomerKeyAdapter) Validate(ctx context.Context, token string) (Principal, error) {
	return a.sdk.Validate(ctx, token)
}
