package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/stripe/stripe-go/v87/webhook"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/httpapi"
	"possums-intake/internal/ledger"
	"possums-intake/internal/operator"
	"possums-intake/internal/payments"
	"possums-intake/internal/stripepay"
	"possums-intake/internal/testfixture"
)

// Public-shape synthetic inputs only. No test reads inherited Stripe inputs,
// sends a paid event, or exercises a provider HTTP operation.
const liveRuntimeSecret = "whsec_SyntheticRuntimeSigningSecretOnly"
const liveRuntimeCustomerKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func liveRuntimeInputs(name string) string {
	switch name {
	case "STRIPE_LIVE_API_KEY":
		return "sk_live_SyntheticRuntimeKeyNotAProviderCredential"
	case "STRIPE_LIVE_WEBHOOK_SECRET":
		return liveRuntimeSecret
	case "STRIPE_LIVE_TAX_CODE":
		return "txcd_12345678"
	case "STRIPE_RETURN_ORIGIN":
		return "https://intake.possums.com"
	default:
		panic("SYNTHETIC_PRIVATE_SENTINEL")
	}
}

func liveRuntimeEnv(selected bool) []string {
	env := []string{"INTAKE_SYNTHETIC_HELPER=1", "OTEL_SDK_DISABLED=true", "LEDGER_ADMIN_KEY=" + syntheticAdminKey}
	for _, name := range []string{"STRIPE_LIVE_API_KEY", "STRIPE_LIVE_WEBHOOK_SECRET", "STRIPE_LIVE_TAX_CODE", "STRIPE_RETURN_ORIGIN"} {
		value := "SYNTHETIC_PRIVATE_SENTINEL"
		if selected {
			value = liveRuntimeInputs(name)
		}
		env = append(env, name+"="+value)
	}
	return env
}

func liveRuntimeCLI(t *testing.T, args []string, env []string) (string, string, bool) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("synthetic executable unavailable")
	}
	cmd := exec.Command(binary, append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	cmd.Env = env
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = cmd.Run()
	return out.String(), stderr.String(), err == nil
}

func noInheritedVolume(t *testing.T) {
	t.Helper()
	t.Setenv(volumeKeyEnv, "")
	if os.Unsetenv(volumeKeyEnv) != nil {
		t.Fatal("synthetic environment unavailable")
	}
}

func newLiveRuntimeState(t *testing.T) (string, *ledger.Service, core.App) {
	t.Helper()
	ctx, dir := context.Background(), filepath.Join(t.TempDir(), "ledger")
	if ledger.InitOperator(ctx, dir, ledger.OperatorCredentials{Email: "synthetic@example.invalid", Password: "synthetic-original-admin-password"}) != nil {
		t.Fatal("synthetic operator initialization unavailable")
	}
	s, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("synthetic open unavailable")
	}
	t.Cleanup(func() {
		if s.Close() != nil {
			t.Error("synthetic closure unavailable")
		}
	})
	app, err := s.OperatorApp()
	if err != nil || initializeWebsite(ctx, app) != nil {
		t.Fatal("synthetic site setup unavailable")
	}
	r, err := testfixture.Create(ctx, s, "liveaccount0000")
	if err != nil {
		t.Fatal("synthetic customer unavailable")
	}
	lookup := sha256.Sum256([]byte("possums-intake/customer-key/lookup/v1\x00" + liveRuntimeCustomerKey))
	password := sha256.Sum256([]byte("possums-intake/customer-key/password/v1\x00" + liveRuntimeCustomerKey))
	r.Set("key_lookup", "v1_"+hex.EncodeToString(lookup[:]))
	r.SetPassword(base64.RawURLEncoding.EncodeToString(password[:]))
	if app.Save(r) != nil {
		t.Fatal("synthetic customer verifier unavailable")
	}
	return dir, s, app
}

// In-memory comparison of every existing data row, including actual native
// credentials/settings, customer verifier, publication/draft and replay/holds.
// Values never enter diagnostics, output, files or test failure messages.
func liveRuntimeSnapshot(t *testing.T, app core.App) map[string][]string {
	t.Helper()
	var tables []struct {
		Name string `db:"name"`
	}
	if app.DB().NewQuery("SELECT name FROM sqlite_schema WHERE type='table' AND name NOT GLOB 'payment_*' ORDER BY name").All(&tables) != nil {
		t.Fatal("synthetic table inventory unavailable")
	}
	out := map[string][]string{}
	for _, table := range tables {
		rows, err := app.DB().NewQuery(`SELECT * FROM "` + strings.ReplaceAll(table.Name, `"`, `""`) + `"`).Rows()
		if err != nil {
			t.Fatal("synthetic comparison read unavailable")
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal("synthetic comparison columns unavailable")
		}
		out[table.Name] = []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if rows.Scan(pointers...) != nil {
				rows.Close()
				t.Fatal("synthetic comparison scan unavailable")
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal("synthetic comparison encoding unavailable")
			}
			out[table.Name] = append(out[table.Name], string(encoded))
		}
		if rows.Err() != nil || rows.Close() != nil {
			t.Fatal("synthetic comparison completion unavailable")
		}
		sort.Strings(out[table.Name])
	}
	return out
}

func TestLivePaymentInitPreservesNativeCustomerPublicationMoneyAndRestart(t *testing.T) {
	noInheritedVolume(t)
	ctx := context.Background()
	dir, s, app := newLiveRuntimeState(t)
	credit := ledger.CreditInput{AccountID: "liveaccount0000", Amount: "100", IdempotencyKey: accountingRuntimeKey(51)}
	if _, err := s.Credit(ctx, credit); err != nil {
		t.Fatal("synthetic funding unavailable")
	}
	if _, err := s.Reserve(ctx, ledger.ReserveInput{AccountID: credit.AccountID, Maximum: "7", TimeoutSeconds: 600, IdempotencyKey: accountingRuntimeKey(52)}); err != nil {
		t.Fatal("synthetic hold unavailable")
	}
	// Keep a valid operator-owned active revision and a distinct unfinished draft.
	selection, err := app.FindRecordById("website_selection", "websitebundle01")
	if err != nil {
		t.Fatal("synthetic selection unavailable")
	}
	var active struct {
		Host  int               `json:"host"`
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal([]byte(selection.GetString("active")), &active) != nil {
		t.Fatal("synthetic publication decode unavailable")
	}
	active.Files["pages.el"] += `
(defun topup-page (view preferences) (page "Old operator page" (ui:p "Sandbox only retained publication")))`
	custom, err := json.Marshal(active)
	if err != nil {
		t.Fatal("synthetic publication encoding unavailable")
	}
	selection.Set("previous", selection.GetRaw("active"))
	selection.Set("active", json.RawMessage(custom))
	inventory, err := app.FindRecordById("website_bundles", "websitebundle01")
	if err != nil {
		t.Fatal("synthetic draft unavailable")
	}
	active.Files["pages.el"] += "\n; distinct unfinished operator draft and preferences\n"
	draft, err := json.Marshal(active)
	if err != nil {
		t.Fatal("synthetic draft encoding unavailable")
	}
	inventory.Set("draft", json.RawMessage(draft))
	if app.Save(selection) != nil || app.Save(inventory) != nil {
		t.Fatal("synthetic publication save unavailable")
	}
	// Open validates/advances the existing clock normally. Keep this synthetic
	// watermark ahead of wall time so complete row equality tests setup itself.
	if _, err := app.DB().NewQuery("UPDATE ledger_meta SET clock_high_water_ms=clock_high_water_ms+60000").Execute(); err != nil {
		t.Fatal("synthetic clock fixture unavailable")
	}
	beforeBalance, err := s.Balance(ctx, credit.AccountID)
	if err != nil {
		t.Fatal("synthetic balance unavailable")
	}
	before := liveRuntimeSnapshot(t, app)
	if s.Close() != nil {
		t.Fatal("synthetic close unavailable")
	}
	for range 2 {
		// Deliberately no admin/operator credentials, no usable Stripe inputs.
		out, stderr, success := liveRuntimeCLI(t, []string{"live-payments-init", "--state", dir}, []string{"INTAKE_SYNTHETIC_HELPER=1", "OTEL_SDK_DISABLED=true", "STRIPE_LIVE_API_KEY=SYNTHETIC_PRIVATE_SENTINEL"})
		if !success || out != "" || stderr != "" {
			t.Fatal("silent additive live setup unavailable; raw output withheld")
		}
	}
	reopened, err := ledger.Open(ctx, dir)
	if err != nil {
		t.Fatal("synthetic restart unavailable")
	}
	defer reopened.Close()
	app, err = reopened.OperatorApp()
	if err != nil || !reflect.DeepEqual(before, liveRuntimeSnapshot(t, app)) {
		t.Fatal("setup/restart changed original native or financial state")
	}
	identity, err := reopened.PaymentIdentity(ctx)
	if err != nil || identity != ledger.PaymentIdentityLiveStripeTax {
		t.Fatal("setup did not admit exact live identity")
	}
	after, err := reopened.Balance(ctx, credit.AccountID)
	if err != nil || !reflect.DeepEqual(beforeBalance, after) {
		t.Fatal("setup changed balance or hold")
	}
	replay, err := reopened.Credit(ctx, credit)
	if err != nil || replay.BalanceAfter != "100" {
		t.Fatal("setup lost credit replay evidence")
	}
	admins, err := app.FindAllRecords("_superusers")
	if err != nil || len(admins) != 1 || !admins[0].ValidatePassword("synthetic-original-admin-password") {
		t.Fatal("setup changed native credentials")
	}
	if app.Settings().Logs.MaxDays != 0 {
		t.Fatal("setup enabled SDK logging")
	}
}

func TestLivePaymentInitRefusesBeforeEffectsAndOwnsOnce(t *testing.T) {
	for _, option := range []string{"--listen=127.0.0.1:0", "--operator", "--operator=false", "--initialize-volume", "--initialize-volume=false", "--stripe-live", "--stripe-live=false", "--unknown", "positional", "--tinfoil-volume=", "--tinfoil-volume=../bad", "--state=relative", "--state=/state/not-ledger"} {
		t.Run(option, func(t *testing.T) {
			t.Setenv(volumeKeyEnv, "SYNTHETIC_PRIVATE_SENTINEL")
			dir := filepath.Join(t.TempDir(), "ledger")
			o := &runtimeObserver{}
			err := executeObserved(context.Background(), []string{"live-payments-init", "--state", dir, "--tinfoil-volume=ledger", option}, o)
			if err == nil || ledger.Failure(err).Code != "PAYMENT_LIVE_OPTIONS_INVALID" {
				t.Fatal("invalid setup option admitted")
			}
			o.want(t, failuretype.RequestInvalid)
			if value, present := os.LookupEnv(volumeKeyEnv); !present || value != "SYNTHETIC_PRIVATE_SENTINEL" {
				t.Fatal("invalid option consumed volume input")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("invalid option created storage")
			}
			assertClosedRuntime(t, err)
		})
	}
	t.Setenv(volumeKeyEnv, "SYNTHETIC_PRIVATE_SENTINEL")
	o := &runtimeObserver{}
	dir := filepath.Join(t.TempDir(), "ledger")
	err := executeLivePaymentInitObserved(context.Background(), []string{"--state", dir}, o)
	if err == nil || ledger.Failure(err).Code != codeVolumeOptions {
		t.Fatal("unnamed volume secret admitted")
	}
	o.want(t, failuretype.RequestInvalid)
	if os.Getenv(volumeKeyEnv) != "SYNTHETIC_PRIVATE_SENTINEL" {
		t.Fatal("unnamed volume input consumed")
	}
	o = &runtimeObserver{}
	err = executeLivePaymentInitObserved(context.Background(), []string{"--state", dir, "--tinfoil-volume=ledger"}, o)
	if err == nil || ledger.Failure(err).Code != codeVolumeKey || ledger.Failure(err).Outcome != "no_volume_request_sent" {
		t.Fatal("invalid managed key reached ledger")
	}
	o.want(t, failuretype.RequestInvalid)
	if _, present := os.LookupEnv(volumeKeyEnv); present {
		t.Fatal("selected invalid key not removed")
	}
	o = &runtimeObserver{}
	err = executeLivePaymentInitObserved(context.Background(), []string{"--state", dir}, o)
	if err == nil || ledger.Failure(err).Code != ledger.CodeMissing {
		t.Fatal("setup initialized missing ledger")
	}
	o.want(t, failuretype.StorageOpenUnknown)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("setup created absent storage")
	}
}

func TestLivePaymentInitManagedExistingOnlyCancellation(t *testing.T) {
	// No mount/socket is contacted. Earlier caller cancellation wins before send;
	// existing volume seam tests pin the unchanged 30-second unlock protocol.
	t.Setenv(volumeKeyEnv, syntheticVolumeKey())
	dir := filepath.Join(t.TempDir(), "ledger")
	if os.Mkdir(dir, 0700) != nil || os.WriteFile(filepath.Join(dir, "data.db"), []byte("original-synthetic-state"), 0600) != nil {
		t.Fatal("synthetic original unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := &runtimeObserver{}
	err := executeLivePaymentInitObserved(ctx, []string{"--state", dir, "--tinfoil-volume=ledger"}, o)
	if err == nil || ledger.Failure(err).Code != codeVolumeCancelled || ledger.Failure(err).Outcome != "no_volume_request_sent" || volumeOperationTimeout(false) != 30*time.Second {
		t.Fatal("existing-only unlock cancellation differs")
	}
	o.want(t, failuretype.Cancelled)
	data, err := os.ReadFile(filepath.Join(dir, "data.db"))
	entries, listErr := os.ReadDir(dir)
	if err != nil || listErr != nil || len(entries) != 1 || string(data) != "original-synthetic-state" {
		t.Fatal("cancelled existing-only setup changed original state")
	}
}

func TestLivePaymentSetupAndServeRefuseV1AndMixedWithoutRepair(t *testing.T) {
	noInheritedVolume(t)
	t.Setenv("LEDGER_ADMIN_KEY", syntheticAdminKey)
	for _, state := range []string{"absent", "v1", "mixed"} {
		t.Run(state, func(t *testing.T) {
			dir, s, app := newLiveRuntimeState(t)
			if state == "v1" && s.InitializeSyntheticPayments(context.Background()) != nil {
				t.Fatal("synthetic v1 unavailable")
			}
			if state == "mixed" {
				if _, err := app.DB().NewQuery("CREATE TABLE payment_meta(singleton INTEGER PRIMARY KEY)").Execute(); err != nil {
					t.Fatal("synthetic mixed schema unavailable")
				}
			}
			if s.Close() != nil {
				t.Fatal("synthetic fixture close unavailable")
			}
			// Hold the listener: a wrong admission must fail BEFORE bind, and
			// must never silently fall back to disabled serving or initialize.
			held, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal("synthetic held listener unavailable")
			}
			defer held.Close()
			o := &runtimeObserver{}
			err = executeStartupInputs(context.Background(), []string{"serve", "--operator", "--stripe-live", "--state", dir, "--listen", held.Addr().String()}, o, nil, liveRuntimeInputs)
			want := ledger.CodePaymentDisabled
			kind := failuretype.PolicyRefused
			if state == "v1" {
				want = "PAYMENT_MODE_MISMATCH"
			}
			if state == "mixed" {
				want, kind = ledger.CodeSchema, failuretype.SchemaRejected
			}
			if err == nil || ledger.Failure(err).Code != want {
				t.Fatal("wrong live mode reached listener or initialized")
			}
			o.want(t, kind)
			if state == "v1" {
				o := &runtimeObserver{}
				err := executeLivePaymentInitObserved(context.Background(), []string{"--state", dir}, o)
				if err == nil || ledger.Failure(err).Code != ledger.CodePaymentConflict {
					t.Fatal("v1 setup changed persisted mode")
				}
				o.want(t, failuretype.PolicyRefused) // one fresh initializer run report
			}
			if state != "absent" {
				out, stderr, success := liveRuntimeCLI(t, []string{"live-payments-init", "--state", dir}, liveRuntimeEnv(false))
				want = ledger.CodePaymentConflict
				if state == "mixed" {
					want = ledger.CodeSchema
				}
				checkFailure(t, out, stderr, success, want)
			}
			if state != "mixed" {
				reopened, err := ledger.Open(context.Background(), dir)
				if err != nil {
					t.Fatal("original state restart unavailable")
				}
				identity, identityErr := reopened.PaymentIdentity(context.Background())
				if state == "absent" && (identityErr == nil || identity != "") || state == "v1" && (identityErr != nil || identity != ledger.PaymentIdentitySynthetic) {
					t.Fatal("admission changed persisted mode")
				}
				if reopened.Close() != nil {
					t.Fatal("synthetic restart close unavailable")
				}
			}
		})
	}
}

func TestLivePaymentFlagSelectionAndConfigBeforeStorage(t *testing.T) {
	for _, mode := range []string{"init", "website-init", "synthetic-serve", "synthetic-payments-init", "live-payments-init", "website-local", "stripe-sandbox-local", "stripe-sandbox-dev", "ledger-replace"} {
		for _, flag := range []string{"--stripe-live", "--stripe-live=false"} {
			dir := filepath.Join(t.TempDir(), "ledger")
			out, stderr, success := liveRuntimeCLI(t, []string{mode, "--state", dir, flag}, liveRuntimeEnv(true))
			if success || out != "" || stderr == "" {
				t.Fatal("unsupported mode accepted live flag")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("unsupported live flag caused storage work")
			}
		}
	}
	for _, flag := range []string{"--stripe-live", "--stripe-live=false"} {
		out, stderr, success := liveRuntimeCLI(t, []string{"serve", "--state", filepath.Join(t.TempDir(), "ledger"), flag}, liveRuntimeEnv(true))
		checkFailure(t, out, stderr, success, "PAYMENT_LIVE_OPTIONS_INVALID")
		out, stderr, success = liveRuntimeCLI(t, []string{"--state", filepath.Join(t.TempDir(), "ledger"), "--operator", flag}, liveRuntimeEnv(true))
		checkFailure(t, out, stderr, success, "PAYMENT_LIVE_OPTIONS_INVALID")
	}
	t.Setenv(volumeKeyEnv, "SYNTHETIC_PRIVATE_SENTINEL")
	for _, field := range []string{"STRIPE_LIVE_API_KEY", "STRIPE_LIVE_WEBHOOK_SECRET", "STRIPE_LIVE_TAX_CODE", "STRIPE_RETURN_ORIGIN"} {
		dir := filepath.Join(t.TempDir(), "ledger")
		o := &runtimeObserver{}
		err := executeStartupInputs(context.Background(), []string{"serve", "--operator", "--stripe-live", "--state", dir, "--tinfoil-volume=ledger"}, o, nil, func(name string) string {
			if name == field {
				return "SYNTHETIC_PRIVATE_SENTINEL"
			}
			return liveRuntimeInputs(name)
		})
		if err == nil || ledger.Failure(err).Code != "STRIPE_LIVE_CONFIGURATION_INVALID" {
			t.Fatal("invalid selected config reached volume/storage")
		}
		o.want(t, failuretype.RequestInvalid)
		if os.Getenv(volumeKeyEnv) != "SYNTHETIC_PRIVATE_SENTINEL" {
			t.Fatal("config refusal consumed volume key")
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("config refusal created storage")
		}
		assertClosedRuntime(t, err)
	}
}

func TestLivePaymentDefaultOffAndSuccessfulAdmissionLaterBindOwned(t *testing.T) {
	noInheritedVolume(t)
	t.Setenv("LEDGER_ADMIN_KEY", syntheticAdminKey)
	dir, s, _ := newLiveRuntimeState(t)
	if s.InitializeLiveStripePayments(context.Background()) != nil || s.Close() != nil {
		t.Fatal("synthetic live fixture unavailable")
	}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("synthetic listener unavailable")
	}
	defer held.Close()
	for _, selection := range []string{"absent", "false", "true"} {
		args := []string{"serve", "--operator", "--state", dir, "--listen", held.Addr().String()}
		if selection != "absent" {
			args = append(args, "--stripe-live="+selection)
		}
		o := &runtimeObserver{}
		err := executeStartupInputs(context.Background(), args, o, nil, func(name string) string {
			if selection != "true" {
				t.Fatal("disabled serving read Stripe input")
			}
			return liveRuntimeInputs(name)
		})
		if err == nil || ledger.Failure(err).Code != codeBind {
			t.Fatal("inert admission did not reach existing listener")
		}
		o.want(t, failuretype.RuntimeServeUnknown)
	}
	out, stderr, success := liveRuntimeCLI(t, []string{"serve", "--operator", "--state", dir, "--listen", held.Addr().String()}, liveRuntimeEnv(false))
	checkFailure(t, out, stderr, success, codeBind)
}

func TestLivePaymentSuccessfulAdmissionDoesNotOwnLaterWebsiteRefusal(t *testing.T) {
	noInheritedVolume(t)
	t.Setenv("LEDGER_ADMIN_KEY", syntheticAdminKey)
	dir, s, app := newLiveRuntimeState(t)
	if s.InitializeLiveStripePayments(context.Background()) != nil {
		t.Fatal("synthetic live initialization unavailable")
	}
	collection, err := app.FindCollectionByNameOrId(ledger.AccountCollectionName)
	if err != nil {
		t.Fatal("synthetic native customer collection unavailable")
	}
	// Native admins retain this supported authority. Ledger accounting remains
	// valid, while the website deliberately refuses unsupported customer MFA.
	collection.MFA.Enabled, collection.OTP.Enabled = true, true
	if app.Save(collection) != nil || s.Close() != nil {
		t.Fatal("synthetic native MFA fixture unavailable")
	}
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("synthetic held listener unavailable")
	}
	defer held.Close()
	o := &runtimeObserver{}
	err = executeStartupInputs(context.Background(), []string{"serve", "--operator", "--stripe-live", "--state", dir, "--listen", held.Addr().String()}, o, nil, liveRuntimeInputs)
	if err == nil || ledger.Failure(err).Code != "SITE_AUTH_MFA_UNSUPPORTED" {
		t.Fatal("later website refusal was erased or reached listener")
	}
	o.want(t, failuretype.PolicyRefused)
	assertClosedRuntime(t, err)
}

type hostileLiveRuntimeError struct{}

func (hostileLiveRuntimeError) Error() string { panic("SYNTHETIC_PRIVATE_SENTINEL") }
func (hostileLiveRuntimeError) Unwrap() error { panic("SYNTHETIC_PRIVATE_SENTINEL") }

func TestLivePaymentDiagnosticClosedFiniteProjection(t *testing.T) {
	private := "SYNTHETIC_PRIVATE_SENTINEL"
	for _, cause := range []error{
		errors.New(private),
		hostileLiveRuntimeError{},
		(*stripepay.Error)(nil),
		(*payments.Error)(nil),
		&stripepay.Error{Code: stripepay.Code(private), Stage: stripepay.Stage(private), Constraint: stripepay.Constraint(private), Outcome: stripepay.Outcome(private), NextStep: stripepay.NextStep(private), HTTPStatus: 599},
		&payments.Error{Code: private, Stage: private, Constraint: private, Outcome: private, NextStep: private, HTTPStatus: 599},
		&stripepay.Error{Code: stripepay.CodeLiveConfiguration, Stage: stripepay.StageLiveConfiguration, Constraint: stripepay.ConstraintLive, Outcome: stripepay.Outcome(private)},
		&payments.Error{Code: "PAYMENT_MODE_MISMATCH", Stage: "payment_admission", Constraint: private, Outcome: "not_attempted"},
	} {
		d := livePaymentDiagnostic(cause)
		if d.Code != "PAYMENT_LIVE_RUNTIME_UNAVAILABLE" || d.Outcome != "unknown" || !strings.Contains(d.NextStep, "cause") || !strings.Contains(d.NextStep, "replay") {
			t.Fatal("untrusted error gained authority")
		}
		assertClosedRuntime(t, d)
	}
	for _, cause := range []error{
		&stripepay.Error{Code: stripepay.CodeLiveConfiguration, Stage: stripepay.StageLiveConfiguration, Constraint: stripepay.ConstraintLive, Outcome: stripepay.OutcomeNotAttempted, NextStep: stripepay.NextStep(private)},
		&stripepay.Error{Code: stripepay.CodeUnexpected, Stage: stripepay.StageLiveConfiguration, Constraint: stripepay.ConstraintUnknown, Outcome: stripepay.OutcomeNotAttempted},
		&payments.Error{Code: "PAYMENT_MODE_MISMATCH", Stage: "payment_admission", Constraint: "persisted_payment_identity_must_match_adapter", Outcome: "not_attempted", NextStep: private},
		&payments.Error{Code: "PAYMENT_PURGE_UNAVAILABLE", Stage: "payment_admission", Constraint: "complete_bounded_startup_purge_required", Outcome: "unavailable", NextStep: private},
		&payments.Error{Code: "PAYMENT_COMPONENT_CAUSE_UNKNOWN", Stage: "payment_admission", Constraint: "component_cause_unknown", Outcome: "unknown", NextStep: private},
		&payments.Error{Code: "PAYMENT_LIVE_CONFIGURATION_INVALID", Stage: "payment_admission", Constraint: "explicit_initialized_live_ledger_and_tax_adapter_required", Outcome: "not_attempted", NextStep: private},
	} {
		d := livePaymentDiagnostic(cause)
		if d.Code == "PAYMENT_LIVE_RUNTIME_UNAVAILABLE" || !d.Kind().Valid() {
			t.Fatal("known closed category erased")
		}
		assertClosedRuntime(t, d)
	}
	core := ledger.CanonicalFailure(&ledger.Diagnostic{Code: ledger.CodeCommit}, failuretype.TransactionCompletionUnknown)
	if livePaymentDiagnostic(core) != core || livePaymentDiagnostic(core).Outcome != "unknown" {
		t.Fatal("core outcome/category erased")
	}
}

func TestLivePaymentAdmissionPrimaryAndSeparateActualClosureOwners(t *testing.T) {
	_, s, app := newLiveRuntimeState(t)
	client, err := stripepay.NewLive(stripepay.LiveConfig{APIKey: liveRuntimeInputs("STRIPE_LIVE_API_KEY"), WebhookSecret: liveRuntimeSecret, TaxCode: liveRuntimeInputs("STRIPE_LIVE_TAX_CODE"), ReturnOrigin: liveRuntimeInputs("STRIPE_RETURN_ORIGIN")})
	if err != nil {
		t.Fatal("synthetic inert client unavailable")
	}
	o := &runtimeObserver{}
	_, primary := payments.NewLiveObserved(context.Background(), s, client, o)
	if primary == nil {
		t.Fatal("absent payment state admitted")
	}
	projected := livePaymentDiagnostic(primary) // presentation owns no observation
	id := app.OnBootstrapClear().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		return errors.New("SYNTHETIC_PRIVATE_SENTINEL")
	})
	defer app.OnBootstrapClear().Unbind(id)
	if closeErr := s.CloseObserved(o); closeErr == nil || projected.Code != ledger.CodePaymentDisabled {
		t.Fatal("closure erased primary")
	}
	o.want(t, failuretype.PolicyRefused, failuretype.StorageCloseUnknown)
	assertClosedRuntime(t, projected)
}

func TestLivePaymentComposedWebsiteWebhookNativeAccounting(t *testing.T) {
	_, s, app := newLiveRuntimeState(t)
	ctx := context.Background()
	if s.InitializeLiveStripePayments(ctx) != nil {
		t.Fatal("synthetic live init unavailable")
	}
	client, err := stripepay.NewLive(stripepay.LiveConfig{APIKey: liveRuntimeInputs("STRIPE_LIVE_API_KEY"), WebhookSecret: liveRuntimeSecret, TaxCode: liveRuntimeInputs("STRIPE_LIVE_TAX_CODE"), ReturnOrigin: liveRuntimeInputs("STRIPE_RETURN_ORIGIN")})
	if err != nil {
		t.Fatal("synthetic inert SDK unavailable")
	}
	o := &runtimeObserver{}
	p, err := payments.NewLiveObserved(ctx, s, client, o)
	if err != nil || !p.IsLive() {
		t.Fatal("synthetic inert admission unavailable")
	}
	accounting, err := httpapi.NewHandler(s, s.Ready, syntheticAdminKey, nil)
	if err != nil {
		t.Fatal("synthetic accounting handler unavailable")
	}
	native, err := operator.NewHandlerObserved(accounting, app, s, o)
	if err != nil {
		t.Fatal("synthetic native handler unavailable")
	}
	host, err := newWebsiteHandlerWithPayments(s, native, o, p)
	if err != nil {
		t.Fatal("synthetic live website unavailable")
	}
	handler := withLivePaymentWebhook(host, p)
	for _, test := range []struct {
		path   string
		status int
	}{{"/healthz", 200}, {"/_/", 200}, {"/api/settings", 401}, {"/payments/stripe/webhook/", 404}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "https://intake.possums.com"+test.path, nil))
		if w.Code != test.status {
			t.Fatal("live composition changed unrelated routes")
		}
	}
	// Only an irrelevant signed event is delivered. It cannot retrieve provider
	// objects even if the fixed SDK origins are reachable on the host.
	raw := []byte(`{"object":"event","id":"evt_synthetic_irrelevant","api_version":"` + stripepay.APIVersion + `","livemode":true,"type":"customer.created","data":{"object":{"object":"customer","private":"SYNTHETIC_PRIVATE_SENTINEL"}}}`)
	for _, valid := range []bool{false, true} {
		r := httptest.NewRequest("POST", "https://intake.possums.com"+payments.WebhookPath, bytes.NewReader(raw))
		if valid {
			r.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: liveRuntimeSecret, Timestamp: time.Now()}).Header)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 400
		if valid {
			want = 200
		}
		if w.Code != want || strings.Contains(w.Body.String(), "SYNTHETIC_PRIVATE") || valid && !strings.Contains(w.Body.String(), `"status":"ignored"`) || len(w.Result().Cookies()) != 0 {
			t.Fatal("raw signature route entered native/website auth or leaked payload")
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "https://intake.possums.com"+payments.WebhookPath+"?selector=synthetic", bytes.NewReader(raw))
	handler.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("webhook query selector admitted")
	}
	// Authenticated live page rendering uses the actual merged customer verifier,
	// HTTPS cookies and CSRF; GET never creates Checkout or retrieves evidence.
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	httpClient := server.Client()
	httpClient.Jar, _ = cookiejar.New(nil)
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, markup := websiteRequest(t, httpClient, server.URL, "GET", "/login", nil)
	if response.StatusCode != 200 {
		t.Fatal("synthetic login page unavailable")
	}
	response, _ = websiteRequest(t, httpClient, server.URL, "POST", "/login", url.Values{"key": {liveRuntimeCustomerKey}, "csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}})
	if response.StatusCode != 303 {
		t.Fatal("synthetic customer login refused")
	}
	response, markup = websiteRequest(t, httpClient, server.URL, "GET", "/topup", nil)
	if response.StatusCode != 200 || !strings.Contains(markup, "Payments are live.") || !strings.Contains(markup, "tax is added") || strings.Contains(markup, "Sandbox only") {
		t.Fatal("admitted capability did not select live page notices")
	}
	// Legacy signature/default nil still disables payments on exact v2 state.
	disabled, err := newWebsiteHandler(s, native, nil)
	if err != nil {
		t.Fatal("disabled website unavailable")
	}
	w = httptest.NewRecorder()
	withLivePaymentWebhook(disabled, nil).ServeHTTP(w, httptest.NewRequest("POST", "https://intake.possums.com"+payments.WebhookPath, bytes.NewReader(raw)))
	if w.Code != 404 {
		t.Fatal("disabled v2 installed webhook")
	}
	balance, err := s.Balance(ctx, "liveaccount0000")
	if err != nil || balance.AccountBalance.Balance != "0" {
		t.Fatal("inert rendering/webhook funded an account")
	}
	var purchases, origins int
	if app.DB().NewQuery("SELECT count(*) FROM payment_purchases").Row(&purchases) != nil || purchases != 0 || app.DB().NewQuery("SELECT count(*) FROM _authOrigins").Row(&origins) != nil || origins != 0 || app.Settings().Logs.MaxDays != 0 {
		t.Fatal("inert composition persisted activity or payment work")
	}
}

func TestLivePaymentLegacyConstructorValidEnvironmentStillDisabled(t *testing.T) {
	for _, name := range []string{"STRIPE_LIVE_API_KEY", "STRIPE_LIVE_WEBHOOK_SECRET", "STRIPE_LIVE_TAX_CODE", "STRIPE_RETURN_ORIGIN"} {
		t.Setenv(name, liveRuntimeInputs(name))
	}
	_, service, _ := newLiveRuntimeState(t)
	if service.InitializeLiveStripePayments(context.Background()) != nil {
		t.Fatal("synthetic live state unavailable")
	}
	host, err := newWebsiteHandler(service, http.NotFoundHandler(), nil)
	if err != nil {
		t.Fatal("legacy website unavailable")
	}
	server := httptest.NewTLSServer(host)
	defer server.Close()
	client := server.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, markup := websiteRequest(t, client, server.URL, "GET", "/login", nil)
	if response.StatusCode != 200 {
		t.Fatal("synthetic login page unavailable")
	}
	response, _ = websiteRequest(t, client, server.URL, "POST", "/login", url.Values{"key": {liveRuntimeCustomerKey}, "csrf_token": {websiteHidden(t, markup, "csrf_token")}, "revision": {websiteHidden(t, markup, "revision")}})
	if response.StatusCode != 303 {
		t.Fatal("synthetic login unavailable")
	}
	response, _ = websiteRequest(t, client, server.URL, "GET", "/topup", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("environment alone enabled legacy payments")
	}
}

func TestLivePaymentCompiledCLIRefusalsAndSilentSetup(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "intake")
	if exec.Command("go", "build", "-p", "1", "-o", binary, ".").Run() != nil {
		t.Fatal("compiled intake fixture unavailable")
	}
	dir, s, _ := newLiveRuntimeState(t)
	if s.Close() != nil {
		t.Fatal("synthetic compiled fixture close unavailable")
	}
	for _, test := range []struct {
		args []string
		code ledger.Code
	}{
		{[]string{"live-payments-init", "--state", dir, "--stripe-live=false"}, "PAYMENT_LIVE_OPTIONS_INVALID"},
		{[]string{"serve", "--state", dir, "--operator", "--stripe-live"}, "STRIPE_LIVE_CONFIGURATION_INVALID"},
		{[]string{"live-payments-init", "--state", dir}, ""},
	} {
		cmd := exec.Command(binary, test.args...)
		cmd.Env = liveRuntimeEnv(false)
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		if test.code != "" {
			checkFailure(t, out.String(), stderr.String(), err == nil, test.code)
		} else if err != nil || out.Len() != 0 || stderr.Len() != 0 {
			t.Fatal("compiled live setup emitted output or refused")
		}
	}
	for _, selected := range []bool{false, true} {
		address := syntheticAddress(t)
		args := []string{"serve", "--operator", "--state", dir, "--listen", address}
		if selected {
			args = append(args, "--stripe-live")
		}
		p := &process{cmd: exec.Command(binary, args...), done: make(chan error, 1), base: "http://" + address}
		p.cmd.Env = liveRuntimeEnv(selected)
		p.cmd.Stdout, p.cmd.Stderr = &p.out, &p.stderr
		if p.cmd.Start() != nil {
			t.Fatal("synthetic compiled serving unavailable")
		}
		go func() { p.done <- p.cmd.Wait() }()
		t.Cleanup(func() { stopProcess(t, p) })
		transport := &http.Transport{Proxy: nil}
		t.Cleanup(transport.CloseIdleConnections)
		client := &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		waitLiveRuntime(t, p, client)
		for _, check := range []struct {
			path   string
			status int
		}{{"/_/", 200}, {"/api/settings", 401}, {"/topup", 303}} { // Anonymous top-up access always requires login.
			response, err := client.Get(p.base + check.path)
			if err != nil {
				t.Fatal("synthetic compiled route unavailable")
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode != check.status {
				t.Fatal("compiled live flag changed route admission")
			}
		}
		raw := []byte(`{"object":"event","id":"evt_synthetic_runtime","api_version":"` + stripepay.APIVersion + `","livemode":true,"type":"customer.created","data":{"object":{"object":"customer"}}}`)
		r, err := http.NewRequest("POST", p.base+payments.WebhookPath, bytes.NewReader(raw))
		if err != nil {
			t.Fatal("synthetic compiled webhook request unavailable")
		}
		r.Header.Set("Stripe-Signature", webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: liveRuntimeSecret, Timestamp: time.Now()}).Header)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal("synthetic compiled webhook unavailable")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		response.Body.Close()
		want := 404
		if selected {
			want = 200
		}
		if readErr != nil || response.StatusCode != want || selected && !bytes.Contains(body, []byte(`"status":"ignored"`)) {
			t.Fatal("compiled signature webhook wiring differs")
		}
		transport.CloseIdleConnections()
		stopProcess(t, p) // Joins the actual same-listener lifecycle, no output/log.
	}
}

func waitLiveRuntime(t *testing.T, p *process, client *http.Client) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticks := time.NewTicker(20 * time.Millisecond)
	defer ticks.Stop()
	for {
		select {
		case <-p.done:
			p.stopped = true
			t.Fatal("synthetic runtime exited before readiness; raw output withheld")
		case <-deadline.C:
			t.Fatal("synthetic runtime readiness unavailable")
		case <-ticks.C:
			response, err := client.Get(p.base + "/healthz")
			if err == nil {
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
		}
	}
}
