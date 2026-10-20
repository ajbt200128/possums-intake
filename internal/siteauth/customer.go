package siteauth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/dbutils"
	"github.com/pocketbase/pocketbase/tools/types"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

// InitializeCustomerAuth is an explicit additive schema operation, never a
// serve/login fallback. It neither creates credentials nor touches ledger rows.
// Existing compatible state is preserved; collisions/incompatible state refuse.
func InitializeCustomerAuth(app core.App) (result error) {
	defer func() {
		if recover() != nil {
			result = customerSchemaFault()
		}
	}()
	if app == nil || app.IsTransactional() {
		return customerSchemaRefused()
	}
	err := app.RunInTransaction(func(tx core.App) error {
		c, err := tx.FindCollectionByNameOrId(CustomerCollection)
		if err == nil {
			if !customerSchema(c) {
				return customerSchemaRefused()
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.FindCollectionByNameOrId(CustomerCollectionID); !errors.Is(err, sql.ErrNoRows) {
			return customerSchemaRefused()
		}
		c = core.NewAuthCollection(CustomerCollection, CustomerCollectionID)
		c.AuthAlert.Enabled = false
		c.PasswordAuth.Enabled = true
		c.PasswordAuth.IdentityFields = []string{"key_lookup"}
		c.AuthRule = types.Pointer("")
		c.AuthToken.Duration = 3600
		c.Fields.GetByName("email").(*core.EmailField).Required = false
		c.Fields.Add(&core.TextField{Name: "key_lookup", Hidden: true, Required: true, Min: 67, Max: 67, Pattern: `^v1_[0-9a-f]{64}$`})
		c.Fields.Add(&core.TextField{Name: "account_id", Required: true, Min: 1, Max: 128})
		c.Indexes = append(c.Indexes, "CREATE UNIQUE INDEX customer_key_lookup ON customer_keys (key_lookup)")
		return tx.Save(c)
	})
	if err != nil {
		var d *ledger.Diagnostic
		if errors.As(err, &d) && d.Code == "SITE_CUSTOMER_SCHEMA_REFUSED" {
			return customerSchemaRefused()
		}
		return classified(customerSchemaFault(), err)
	}
	return nil
}

func customerSchema(c *core.Collection) bool {
	if c == nil || c.Name != CustomerCollection || c.Id != CustomerCollectionID || !c.IsAuth() || len(c.Fields) != 8 {
		return false
	}
	lookup, ok := c.Fields.GetByName("key_lookup").(*core.TextField)
	if !ok || !lookup.Hidden || !lookup.Required || lookup.Min != 67 || lookup.Max != 67 || lookup.Pattern != `^v1_[0-9a-f]{64}$` {
		return false
	}
	binding, ok := c.Fields.GetByName("account_id").(*core.TextField)
	if !ok || !binding.Required || binding.Min != 1 || binding.Max != 128 || binding.Pattern != "" {
		return false
	}
	password, ok := c.Fields.GetByName("password").(*core.PasswordField)
	if !ok || !password.Hidden || !password.Required || password.Min > 43 || password.Max > 0 && password.Max < 43 || password.Pattern != "" {
		return false
	}
	if _, ok := c.Fields.GetByName("tokenKey").(*core.TextField); !ok {
		return false
	}
	email, ok := c.Fields.GetByName("email").(*core.EmailField)
	if !ok || email.Required {
		return false
	}
	index, unique := dbutils.FindSingleColumnUniqueIndex(c.Indexes, "key_lookup")
	return unique && index.TableName == CustomerCollection && index.Where == "" && len(c.PasswordAuth.IdentityFields) == 1 && c.PasswordAuth.IdentityFields[0] == "key_lookup"
}

func customerSchemaRefused() *ledger.Diagnostic {
	d := refused("SITE_CUSTOMER_SCHEMA_REFUSED", "customer_auth_schema", "explicit_compatible_customer_schema", failuretype.PolicyRefused)
	d.Outcome = "no_effect"
	d.NextStep = "Use explicit initialization outside a transaction; inspect incompatible or colliding schema privately. Do not reset credentials or financial state."
	return d
}

func customerSchemaFault() *ledger.Diagnostic {
	d := refused("SITE_CUSTOMER_SCHEMA_UNAVAILABLE", "customer_auth_schema", "fixed_auth_schema_cause_unknown", failuretype.NativeWriteUnknown)
	d.Outcome = "customer_auth_schema_unconfirmed"
	d.NextStep = "Inspect the dedicated auth schema privately before explicit initialization; never reset accounts or restore financial state to recover website auth."
	return d
}

// CustomerKeyAdapter converts only the approved key protocol into private final
// SDK authentication. Account binding is returned from the same SDK-validated
// record, never from the browser or a second stale record lookup.
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
