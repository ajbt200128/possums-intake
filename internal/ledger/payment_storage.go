package ledger

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
)

// Independent, explicitly opted-in synthetic schema. Never alter schemaV3 or
// ledger_meta's version to enable this feature. All keys lack hidden rowids.
const paymentSchemaV1 = `
CREATE TABLE payment_meta (
 singleton INTEGER PRIMARY KEY NOT NULL CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=1),
 identity TEXT NOT NULL CHECK(identity='synthetic-only'),
 sales_retention_days INTEGER NOT NULL CHECK(sales_retention_days=90)
) WITHOUT ROWID;
CREATE TABLE payment_sales (
 sale_id TEXT PRIMARY KEY NOT NULL CHECK(length(sale_id)=64 AND sale_id NOT GLOB '*[^0-9a-f]*'),
 account_id TEXT NOT NULL CHECK(length(account_id)=15 AND account_id NOT GLOB '*[^a-z0-9]*'),
 sale_date TEXT NOT NULL CHECK(length(sale_date)=10),
 currency TEXT NOT NULL CHECK(currency='usd'),
 gross_minor TEXT NOT NULL CHECK(typeof(gross_minor)='text' AND gross_minor IN ('500','1000','2500','5000')),
 credit_units TEXT NOT NULL CHECK(typeof(credit_units)='text' AND ((gross_minor='500' AND credit_units='5000000') OR (gross_minor='1000' AND credit_units='10000000') OR (gross_minor='2500' AND credit_units='25000000') OR (gross_minor='5000' AND credit_units='50000000')))
) WITHOUT ROWID;
CREATE TABLE payment_purchases (
 purchase_ref TEXT PRIMARY KEY NOT NULL CHECK(length(purchase_ref)=64 AND purchase_ref NOT GLOB '*[^0-9a-f]*'),
 form_attempt TEXT NOT NULL UNIQUE CHECK(length(form_attempt)=64 AND form_attempt NOT GLOB '*[^0-9a-f]*'),
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 currency TEXT NOT NULL CHECK(currency='usd'),
 charge_minor TEXT NOT NULL CHECK(typeof(charge_minor)='text' AND charge_minor IN ('500','1000','2500','5000')),
 credit_units TEXT NOT NULL CHECK(typeof(credit_units)='text' AND ((charge_minor='500' AND credit_units='5000000') OR (charge_minor='1000' AND credit_units='10000000') OR (charge_minor='2500' AND credit_units='25000000') OR (charge_minor='5000' AND credit_units='50000000'))),
 issued_ms INTEGER NOT NULL CHECK(typeof(issued_ms)='integer' AND issued_ms>0 AND issued_ms<=9998271999999),
 unlink_at INTEGER NOT NULL CHECK(typeof(unlink_at)='integer' AND unlink_at=issued_ms+1728000000),
 checkout_expires_at INTEGER NOT NULL CHECK(typeof(checkout_expires_at)='integer' AND checkout_expires_at=((issued_ms+3600000)/1000)*1000),
 form_issued_ms INTEGER NOT NULL CHECK(typeof(form_issued_ms)='integer' AND form_issued_ms>0 AND form_issued_ms<=issued_ms),
 form_expires_at INTEGER NOT NULL CHECK(typeof(form_expires_at)='integer' AND form_expires_at>issued_ms AND form_expires_at>form_issued_ms AND form_expires_at<=form_issued_ms+600000),
 create_key TEXT NOT NULL UNIQUE CHECK(length(create_key)=64 AND create_key NOT GLOB '*[^0-9a-f]*'),
 create_state TEXT NOT NULL CHECK(create_state IN ('prepared','attempted','unknown','known')),
 stripe_session_id TEXT NULL UNIQUE CHECK(stripe_session_id IS NULL OR (length(stripe_session_id) BETWEEN 4 AND 203 AND substr(stripe_session_id,1,3)='cs_' AND stripe_session_id NOT GLOB '*[^A-Za-z0-9_]*')),
 stripe_payment_intent_id TEXT NULL UNIQUE CHECK(stripe_payment_intent_id IS NULL OR (length(stripe_payment_intent_id) BETWEEN 4 AND 203 AND substr(stripe_payment_intent_id,1,3)='pi_' AND stripe_payment_intent_id NOT GLOB '*[^A-Za-z0-9_]*')),
 session_created_ms INTEGER NULL CHECK(session_created_ms IS NULL OR (typeof(session_created_ms)='integer' AND session_created_ms>0 AND session_created_ms%1000=0 AND session_created_ms>=issued_ms-60000 AND session_created_ms<=issued_ms+360000)),
 sale_id TEXT NULL UNIQUE REFERENCES payment_sales(sale_id) ON DELETE RESTRICT,
 funding_state TEXT NOT NULL CHECK(funding_state IN ('unfunded','funded','blocked')),
 CHECK((create_state='known' AND stripe_session_id IS NOT NULL AND session_created_ms IS NOT NULL) OR (create_state!='known' AND stripe_session_id IS NULL AND stripe_payment_intent_id IS NULL AND session_created_ms IS NULL)),
 CHECK(funding_state='unfunded' OR create_state='known'),
 CHECK((funding_state='funded' AND sale_id IS NOT NULL AND stripe_payment_intent_id IS NOT NULL) OR (funding_state!='funded' AND sale_id IS NULL))
) WITHOUT ROWID;
CREATE INDEX payment_purchases_account ON payment_purchases(account_id,unlink_at);
CREATE INDEX payment_purchases_prune ON payment_purchases(unlink_at);
CREATE INDEX payment_sales_prune ON payment_sales(sale_date);
`

// Live state is created only by explicit absent-state setup. V1 is immutable;
// neither initializer migrates/relabels existing synthetic purchases as live.
const paymentSchemaV2 = `
CREATE TABLE payment_meta (
 singleton INTEGER PRIMARY KEY NOT NULL CHECK(singleton=1),
 schema_version INTEGER NOT NULL CHECK(schema_version=2),
 identity TEXT NOT NULL CHECK(identity='live-stripe-tax'),
 sales_retention_days INTEGER NOT NULL CHECK(sales_retention_days=20),
 tax_behavior TEXT NOT NULL CHECK(tax_behavior='exclusive')
) WITHOUT ROWID;
CREATE TABLE payment_sales (
 sale_id TEXT PRIMARY KEY NOT NULL CHECK(length(sale_id)=64 AND sale_id NOT GLOB '*[^0-9a-f]*'),
 account_id TEXT NOT NULL CHECK(length(account_id)=15 AND account_id NOT GLOB '*[^a-z0-9]*'),
 sale_date TEXT NOT NULL CHECK(length(sale_date)=10),
 currency TEXT NOT NULL CHECK(currency='usd'),
 principal_minor TEXT NOT NULL CHECK(typeof(principal_minor)='text' AND principal_minor IN ('500','1000','2500','5000')),
 tax_minor TEXT NOT NULL CHECK(typeof(tax_minor)='text'),
 gross_minor TEXT NOT NULL CHECK(typeof(gross_minor)='text'),
 credit_units TEXT NOT NULL CHECK(typeof(credit_units)='text' AND ((principal_minor='500' AND credit_units='5000000') OR (principal_minor='1000' AND credit_units='10000000') OR (principal_minor='2500' AND credit_units='25000000') OR (principal_minor='5000' AND credit_units='50000000')))
) WITHOUT ROWID;
CREATE TABLE payment_purchases (
 purchase_ref TEXT PRIMARY KEY NOT NULL CHECK(length(purchase_ref)=64 AND purchase_ref NOT GLOB '*[^0-9a-f]*'),
 form_attempt TEXT NOT NULL UNIQUE CHECK(length(form_attempt)=64 AND form_attempt NOT GLOB '*[^0-9a-f]*'),
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 currency TEXT NOT NULL CHECK(currency='usd'),
 charge_minor TEXT NOT NULL CHECK(typeof(charge_minor)='text' AND charge_minor IN ('500','1000','2500','5000')),
 credit_units TEXT NOT NULL CHECK(typeof(credit_units)='text' AND ((charge_minor='500' AND credit_units='5000000') OR (charge_minor='1000' AND credit_units='10000000') OR (charge_minor='2500' AND credit_units='25000000') OR (charge_minor='5000' AND credit_units='50000000'))),
 tax_code TEXT NOT NULL CHECK(length(tax_code)=13 AND substr(tax_code,1,5)='txcd_' AND substr(tax_code,6) NOT GLOB '*[^0-9]*'),
 issued_ms INTEGER NOT NULL CHECK(typeof(issued_ms)='integer' AND issued_ms>0 AND issued_ms<=9998271999999),
 unlink_at INTEGER NOT NULL CHECK(typeof(unlink_at)='integer' AND unlink_at=issued_ms+1728000000),
 checkout_expires_at INTEGER NOT NULL CHECK(typeof(checkout_expires_at)='integer' AND checkout_expires_at=((issued_ms+3600000)/1000)*1000),
 form_issued_ms INTEGER NOT NULL CHECK(typeof(form_issued_ms)='integer' AND form_issued_ms>0 AND form_issued_ms<=issued_ms),
 form_expires_at INTEGER NOT NULL CHECK(typeof(form_expires_at)='integer' AND form_expires_at>issued_ms AND form_expires_at>form_issued_ms AND form_expires_at<=form_issued_ms+600000),
 create_key TEXT NOT NULL UNIQUE CHECK(length(create_key)=64 AND create_key NOT GLOB '*[^0-9a-f]*'),
 create_state TEXT NOT NULL CHECK(create_state IN ('prepared','attempted','unknown','known')),
 stripe_session_id TEXT NULL UNIQUE CHECK(stripe_session_id IS NULL OR (length(stripe_session_id) BETWEEN 4 AND 203 AND substr(stripe_session_id,1,3)='cs_' AND stripe_session_id NOT GLOB '*[^A-Za-z0-9_]*')),
 stripe_payment_intent_id TEXT NULL UNIQUE CHECK(stripe_payment_intent_id IS NULL OR (length(stripe_payment_intent_id) BETWEEN 4 AND 203 AND substr(stripe_payment_intent_id,1,3)='pi_' AND stripe_payment_intent_id NOT GLOB '*[^A-Za-z0-9_]*')),
 session_created_ms INTEGER NULL CHECK(session_created_ms IS NULL OR (typeof(session_created_ms)='integer' AND session_created_ms>0 AND session_created_ms%1000=0 AND session_created_ms>=issued_ms-60000 AND session_created_ms<=issued_ms+360000)),
 sale_id TEXT NULL UNIQUE REFERENCES payment_sales(sale_id) ON DELETE RESTRICT,
 funding_state TEXT NOT NULL CHECK(funding_state IN ('unfunded','funded','blocked')),
 CHECK((create_state='known' AND stripe_session_id IS NOT NULL AND session_created_ms IS NOT NULL) OR (create_state!='known' AND stripe_session_id IS NULL AND stripe_payment_intent_id IS NULL AND session_created_ms IS NULL)),
 CHECK(funding_state='unfunded' OR create_state='known'),
 CHECK((funding_state='funded' AND sale_id IS NOT NULL AND stripe_payment_intent_id IS NOT NULL) OR (funding_state!='funded' AND sale_id IS NULL))
) WITHOUT ROWID;
CREATE INDEX payment_purchases_account ON payment_purchases(account_id,unlink_at);
CREATE INDEX payment_purchases_prune ON payment_purchases(unlink_at);
`

const paymentColumns = "purchase_ref,form_attempt,account_id,currency,charge_minor,credit_units,issued_ms,unlink_at,checkout_expires_at,form_issued_ms,form_expires_at,create_key,create_state,stripe_session_id,stripe_payment_intent_id,session_created_ms,sale_id,funding_state"
const paymentSaleColumns = "sale_id,account_id,sale_date,currency,gross_minor,credit_units"

type paymentSale struct {
	ID             string `db:"sale_id"`
	AccountID      string `db:"account_id"`
	Date           string `db:"sale_date"`
	Currency       string `db:"currency"`
	GrossMinor     Money  `db:"gross_minor"`
	CreditUnits    Money  `db:"credit_units"`
	PrincipalMinor Money  `db:"principal_minor"`
	TaxMinor       Money  `db:"tax_minor"`
}

func purchaseColumns(identity PaymentIdentity) string {
	if identity == PaymentIdentityLiveStripeTax {
		return paymentColumns + ",tax_code"
	}
	return paymentColumns + ",'' AS tax_code"
}
func saleColumns(identity PaymentIdentity) string {
	if identity == PaymentIdentityLiveStripeTax {
		return paymentSaleColumns + ",principal_minor,tax_minor"
	}
	return paymentSaleColumns + ",gross_minor AS principal_minor,'0' AS tax_minor"
}

// validatePaymentDDL accepts only completely absent or exactly known optional
// schema. Unknown payment-prefixed tables, indexes, triggers and views fail too.
func validatePaymentDDL(query func(string, dbx.Params) *dbx.Query) (bool, error) {
	identity, err := validatedPaymentIdentity(query)
	return identity != "", err
}

func validatedPaymentIdentity(query func(string, dbx.Params) *dbx.Query) (PaymentIdentity, error) {
	var objects int
	if err := query("SELECT count(*) FROM sqlite_schema WHERE (lower(name) GLOB 'payment_*' OR lower(tbl_name) GLOB 'payment_*') AND sql IS NOT NULL", nil).Row(&objects); err != nil {
		return "", err
	}
	if objects == 0 {
		return "", nil
	}
	// Counts and every SQL byte must match one complete version, never a mix.
	ddl := paymentSchemaV1
	identity := PaymentIdentitySynthetic
	if objects == 5 {
		ddl = paymentSchemaV2
		identity = PaymentIdentityLiveStripeTax
	} else if objects != 6 {
		return "", schemaFailure(schemaPaymentLayout)
	}
	for _, statement := range strings.Split(ddl, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		name := strings.Fields(statement)[2]
		var stored string
		err := query("SELECT sql FROM sqlite_schema WHERE name={:name}", dbx.Params{"name": name}).Row(&stored)
		if err == sql.ErrNoRows {
			return "", schemaFailure(schemaPaymentLayout)
		}
		if err != nil {
			return "", err
		}
		if stored != statement {
			return "", schemaFailure(schemaPaymentLayout)
		}
	}
	var count, version, days int
	var storedIdentity string
	if err := query("SELECT count(*) FROM payment_meta", nil).Row(&count); err != nil {
		return "", err
	}
	if count != 1 {
		return "", schemaFailure(schemaPaymentMetadata)
	}
	if err := query("SELECT schema_version,identity,sales_retention_days FROM payment_meta WHERE singleton=1", nil).Row(&version, &storedIdentity, &days); err != nil {
		if err == sql.ErrNoRows {
			return "", schemaFailure(schemaPaymentMetadata)
		}
		return "", err
	}
	if identity == PaymentIdentitySynthetic {
		if version != 1 || storedIdentity != string(identity) || days != paymentSalesDays {
			return "", schemaFailure(schemaPaymentMetadata)
		}
	} else {
		var behavior string
		if err := query("SELECT tax_behavior FROM payment_meta WHERE singleton=1", nil).Row(&behavior); err != nil {
			return "", err
		}
		if version != 2 || storedIdentity != string(identity) || days != 20 || behavior != "exclusive" {
			return "", schemaFailure(schemaPaymentMetadata)
		}
	}
	return identity, nil
}

// InitializeSyntheticPayments is the explicit synthetic creation path. It performs no
// credit, credentials, collection change, repair, live-mode setup or clock reset.
func (s *Service) InitializeSyntheticPayments(ctx context.Context) error {
	return s.run(ctx, func(tx *transaction) error {
		if err := validateDDL(tx.query); err != nil {
			return err
		}
		present, err := validatePaymentDDL(tx.query)
		if err != nil {
			return err
		}
		if !present {
			if err := tx.exec(paymentSchemaV1, nil); err != nil {
				return err
			}
			if err := tx.exec("INSERT INTO payment_meta(singleton,schema_version,identity,sales_retention_days) VALUES(1,1,'synthetic-only',90)", nil); err != nil {
				return err
			}
		}
		return tx.validatePaymentRows()
	}, false)
}

// InitializeLiveStripePayments adds only absent optional v2 payment state. It
// never migrates v1, resets accounting time, or changes native/site credentials.
func (s *Service) InitializeLiveStripePayments(ctx context.Context) error {
	return s.run(ctx, func(tx *transaction) error {
		if err := validateDDL(tx.query); err != nil {
			return err
		}
		identity, err := validatedPaymentIdentity(tx.query)
		if err != nil {
			return err
		}
		if identity == PaymentIdentitySynthetic {
			return diagnostic(CodePaymentConflict)
		}
		if identity == "" {
			if err := tx.exec(paymentSchemaV2, nil); err != nil {
				return err
			}
			if err := tx.exec("INSERT INTO payment_meta(singleton,schema_version,identity,sales_retention_days,tax_behavior) VALUES(1,2,'live-stripe-tax',20,'exclusive')", nil); err != nil {
				return err
			}
		}
		return tx.validatePaymentRows()
	}, false)
}

// PaymentIdentity validates the exact optional schema without creating state or
// advancing the clock. No app handle or process-global payment mode is exposed.
func (s *Service) PaymentIdentity(ctx context.Context) (PaymentIdentity, error) {
	var identity PaymentIdentity
	err := s.run(ctx, func(tx *transaction) error {
		var err error
		identity, err = tx.paymentIdentity()
		return err
	}, false)
	if err != nil {
		return "", err
	}
	return identity, nil
}
func (tx *transaction) paymentIdentity() (PaymentIdentity, error) {
	identity, err := validatedPaymentIdentity(tx.query)
	if err == nil && identity == "" {
		return "", diagnostic(CodePaymentDisabled)
	}
	return identity, err
}

func (tx *transaction) requirePayments() error {
	present, err := validatePaymentDDL(tx.query)
	if err != nil {
		return err
	}
	if !present {
		return diagnostic(CodePaymentDisabled)
	}
	return nil
}
func validatePurchase(p PaymentPurchase, identity PaymentIdentity) error {
	if (identity == PaymentIdentitySynthetic && p.TaxCode != "") || (identity == PaymentIdentityLiveStripeTax && !paymentTaxCodePattern.MatchString(p.TaxCode)) {
		return diagnostic(CodeCorrupt)
	}
	if !paymentTokenPattern.MatchString(p.Reference) || !paymentTokenPattern.MatchString(p.FormAttempt) || !paymentTokenPattern.MatchString(p.CreateKey) || !validAccount(p.AccountID) || !paymentTermsValid(p.Currency, p.GrossMinor, p.CreditUnits) || p.IssuedAt <= 0 || int64(p.IssuedAt) > paymentMaxTime-paymentLinkMS || int64(p.UnlinkAt) != int64(p.IssuedAt)+paymentLinkMS || int64(p.CheckoutExpiresAt) != (int64(p.IssuedAt)+paymentCheckoutMS)/1000*1000 || p.FormIssuedAt <= 0 || p.FormIssuedAt > p.IssuedAt || p.FormExpiresAt <= p.IssuedAt || p.FormExpiresAt <= p.FormIssuedAt || int64(p.FormExpiresAt)-int64(p.FormIssuedAt) > paymentFormMS {
		return diagnostic(CodeCorrupt)
	}
	switch p.CreateState {
	case "prepared", "attempted", "unknown":
		if p.SessionID != nil || p.PaymentIntentID != nil || p.SessionCreatedAt != nil {
			return diagnostic(CodeCorrupt)
		}
	case "known":
		if p.SessionID == nil || !paymentSessionPattern.MatchString(*p.SessionID) || p.SessionCreatedAt == nil || *p.SessionCreatedAt <= 0 || int64(*p.SessionCreatedAt)%1000 != 0 || int64(*p.SessionCreatedAt) < int64(p.IssuedAt)-FutureSkewMS || int64(*p.SessionCreatedAt) > int64(p.IssuedAt)+paymentCreateMS+FutureSkewMS {
			return diagnostic(CodeCorrupt)
		}
	default:
		return diagnostic(CodeCorrupt)
	}
	if p.FundingState != "unfunded" && p.CreateState != "known" {
		return diagnostic(CodeCorrupt)
	}
	if p.PaymentIntentID != nil && !paymentIntentPattern.MatchString(*p.PaymentIntentID) {
		return diagnostic(CodeCorrupt)
	}
	switch p.FundingState {
	case "funded":
		if p.SaleID == nil || !paymentTokenPattern.MatchString(*p.SaleID) || p.PaymentIntentID == nil {
			return diagnostic(CodeCorrupt)
		}
	case "unfunded", "blocked":
		if p.SaleID != nil {
			return diagnostic(CodeCorrupt)
		}
	default:
		return diagnostic(CodeCorrupt)
	}
	return nil
}
func validateSale(s paymentSale, identity PaymentIdentity) error {
	principal := s.GrossMinor
	if identity == PaymentIdentityLiveStripeTax {
		principal = s.PrincipalMinor
		if !paymentTaxTotal(principal, s.TaxMinor, s.GrossMinor) {
			return diagnostic(CodeCorrupt)
		}
	}
	date, err := time.Parse("2006-01-02", s.Date)
	if err != nil || date.Format("2006-01-02") != s.Date || date.Year() < 1970 || !paymentTokenPattern.MatchString(s.ID) || !validAccount(s.AccountID) || !paymentTermsValid(s.Currency, principal, s.CreditUnits) {
		return diagnostic(CodeCorrupt)
	}
	return nil
}
func (tx *transaction) payment(field, value string) (PaymentPurchase, error) {
	identity, err := tx.paymentIdentity()
	if err != nil {
		return PaymentPurchase{}, err
	}
	return tx.paymentForIdentity(identity, field, value)
}
func (tx *transaction) paymentForIdentity(identity PaymentIdentity, field, value string) (PaymentPurchase, error) {
	var p PaymentPurchase
	err := tx.query("SELECT "+purchaseColumns(identity)+" FROM payment_purchases WHERE "+field+"={:value}", dbx.Params{"value": value}).One(&p)
	if err != nil {
		return p, err
	}
	if err := validatePurchase(p, identity); err != nil {
		return p, err
	}
	if p.SaleID != nil {
		var sale paymentSale
		err := tx.query("SELECT "+saleColumns(identity)+" FROM payment_sales WHERE sale_id={:id}", dbx.Params{"id": *p.SaleID}).One(&sale)
		if err == sql.ErrNoRows {
			return p, diagnostic(CodeCorrupt)
		}
		if err != nil {
			return p, err
		}
		if err := validateSale(sale, identity); err != nil {
			return p, err
		}
		if sale.AccountID != p.AccountID || sale.Currency != p.Currency || sale.PrincipalMinor != p.GrossMinor || sale.CreditUnits != p.CreditUnits || sale.Date < paymentSaleDate(int64(*p.SessionCreatedAt)) || sale.Date > paymentSaleDate(int64(p.CheckoutExpiresAt)) {
			return p, diagnostic(CodeCorrupt)
		}
	}
	return p, nil
}
func (tx *transaction) validatePaymentRows() error {
	identity, err := validatedPaymentIdentity(tx.query)
	if err != nil || identity == "" {
		return err
	}
	var purchases []PaymentPurchase
	if err := tx.query("SELECT "+purchaseColumns(identity)+" FROM payment_purchases", nil).All(&purchases); err != nil {
		return err
	}
	for _, p := range purchases {
		if _, err := tx.paymentForIdentity(identity, "purchase_ref", p.Reference); err != nil {
			return err
		}
		if _, err := tx.account(p.AccountID); err != nil {
			if d, ok := err.(*Diagnostic); ok && d.Code == CodeAccount {
				return diagnostic(CodeCorrupt)
			}
			return err
		}
	}
	var sales []paymentSale
	if err := tx.query("SELECT "+saleColumns(identity)+" FROM payment_sales", nil).All(&sales); err != nil {
		return err
	}
	for _, sale := range sales {
		if err := validateSale(sale, identity); err != nil {
			return err
		}
		if identity == PaymentIdentityLiveStripeTax {
			var owners int
			if err := tx.query("SELECT count(*) FROM payment_purchases WHERE sale_id={:id} AND funding_state='funded'", dbx.Params{"id": sale.ID}).Row(&owners); err != nil {
				return err
			}
			if owners != 1 {
				return diagnostic(CodeCorrupt)
			}
		}
	}
	return nil
}
