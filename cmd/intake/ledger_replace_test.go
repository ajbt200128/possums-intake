package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

func replaceArgs() []string {
	return []string{"--state", replaceState, "--tinfoil-volume", "ledger", "--operator", "--discard-existing-ledger"}
}

func replaceCredentials() ledger.OperatorCredentials {
	return ledger.OperatorCredentials{Email: "replacement@example.invalid", Password: "SYNTHETIC_REPLACEMENT_PASSWORD"}
}

func assertReplaceFailure(t *testing.T, err error, code ledger.Code, outcome string) {
	t.Helper()
	if err == nil {
		t.Fatal("replacement unexpectedly accepted")
	}
	d := ledger.Failure(err)
	if d.Code != code || d.Outcome != outcome || d.Stage == "" || d.Constraint == "" || d.NextStep == "" || !d.Kind().Valid() {
		t.Fatalf("replacement closed evidence differs: %s %s", d.Code, d.Outcome)
	}
	encoded, e := json.Marshal(d)
	if e != nil || bytes.Contains(encoded, []byte("HOSTILE_PRIVATE_SENTINEL")) || bytes.Contains(encoded, []byte(replaceCredentials().Password)) || bytes.Contains(encoded, []byte(replaceCredentials().Email)) {
		t.Fatal("replacement diagnostic leaked synthetic hostile input")
	}
}

func TestLedgerReplaceExactOptionsBeforeInputsOrVolume(t *testing.T) {
	valid := replaceArgs()
	cases := [][]string{nil, {"--discard-existing-ledger"}}
	for i := range valid {
		cases = append(cases, append(append([]string{}, valid[:i]...), valid[i+1:]...))
	}
	for _, extra := range []string{"--discard-existing-ledger", "--operator", "--discard-existing-ledger=false", "--discard-existing-ledger=true", "--listen", "--initialize-volume", "--payments", "--diagnostics", "--unknown", "HOSTILE_PRIVATE_SENTINEL", "--"} {
		cases = append(cases, append(append([]string{}, valid...), extra))
	}
	for _, path := range []string{"/state", "/state/ledger/", "/state/other/../ledger", "/tmp/ledger", "", "HOSTILE_PRIVATE_SENTINEL"} {
		args := replaceArgs()
		args[1] = path
		cases = append(cases, args)
	}
	for _, volume := range []string{"", "other", "Ledger", "ledger/"} {
		args := replaceArgs()
		args[3] = volume
		cases = append(cases, args)
	}
	for _, args := range cases {
		o := &runtimeObserver{}
		err := executeLedgerReplaceUsing(context.Background(), args, o,
			func() ledger.OperatorCredentials {
				t.Fatal("invalid argv reached private input")
				return ledger.OperatorCredentials{}
			},
			func(context.Context, string, string, bool) error { t.Fatal("invalid argv reached volume"); return nil },
			func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
				t.Fatal("invalid argv reached discard")
				return nil
			})
		assertReplaceFailure(t, err, "LEDGER_REPLACE_OPTIONS_INVALID", "old_state_not_discarded")
		o.want(t, failuretype.RequestInvalid)
	}
	if !validReplaceArgs(valid) || !validReplaceArgs([]string{"--operator", "--discard-existing-ledger", "--tinfoil-volume", "ledger", "--state", replaceState}) {
		t.Fatal("canonical explicit options refused")
	}
}

func TestLedgerReplaceOperatorValidationBeforeVolume(t *testing.T) {
	for _, input := range []ledger.OperatorCredentials{
		{}, {Email: "HOSTILE_PRIVATE_SENTINEL", Password: "HOSTILE_PRIVATE_SENTINEL"},
		{Email: "test@example.invalid", Password: "short"},
		{Email: "test@example.invalid", Password: strings.Repeat("x", 72)},
		{Email: "test@example.invalid", Password: strings.Repeat("é", 36)},
		{Email: "test@example.invalid", Password: "invalid\xffpassword"},
	} {
		o := &runtimeObserver{}
		err := executeLedgerReplaceUsing(context.Background(), replaceArgs(), o,
			func() ledger.OperatorCredentials { return input },
			func(context.Context, string, string, bool) error {
				t.Fatal("invalid operator reached volume")
				return nil
			},
			func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
				t.Fatal("invalid operator reached replacement")
				return nil
			})
		assertReplaceFailure(t, err, "LEDGER_REPLACE_OPERATOR_INVALID", "old_state_not_discarded")
		o.want(t, failuretype.RequestInvalid)
	}
}

func TestLedgerReplaceUnlockOnlyAndVolumeFailureStops(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Setenv(volumeKeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
		calls, replaced := 0, 0
		err := executeLedgerReplaceUsing(context.Background(), replaceArgs(), nil, replaceCredentials,
			func(ctx context.Context, name, state string, initialize bool) error {
				if initialize || name != "ledger" || state != replaceState {
					t.Fatal("wrong volume selection")
				}
				return prepareManagedVolumeUsing(ctx, name, state, initialize, func(_ context.Context, _, _ string, packet []byte, p *volumeProgress) error {
					calls++
					if len(packet) != 34 || packet[0] != 2 || packet[1] != 'u' {
						t.Fatal("replacement sent initialization packet")
					}
					if fail {
						p.sent = true
						return volumeFailure(codeVolumeExchange, "volume_send", "single_complete_packet_cause_unknown", p)
					}
					return nil // controlled seam, NOT real kernel mount evidence
				})
			}, func(context.Context, string, ledger.OperatorCredentials, failureowner.Observer) error {
				replaced++
				return nil
			})
		if calls != 1 || fail && (err == nil || replaced != 0) || !fail && (err != nil || replaced != 1) {
			t.Fatal("volume stage repeated or failure bypassed")
		}
		if fail && ledger.Failure(err).Outcome != "volume_outcome_unknown" {
			t.Fatal("volume outcome erased")
		}
	}
}

// Independent historical fixture: financial SQL comes from the already pinned
// refusal fixture, collection construction from predecessor 9a8b6ba, NOT from
// the production admission constants/models being tested.
func replaceV2Fixture(t *testing.T, mutate func(core.App)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ledger")
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
	if app.Bootstrap() != nil {
		t.Fatal("synthetic predecessor bootstrap unavailable")
	}
	accounts := core.NewBaseCollection("ledger_accounts", "ledgeraccounts1")
	accounts.Fields.Add(&core.TextField{Name: "account_id", Required: true, Max: 128})
	accounts.Fields.Add(&core.TextField{Name: "balance", Required: true, Max: 20, Pattern: `^(0|[1-9][0-9]{0,19})$`})
	accounts.Fields.Add(&core.BoolField{Name: "enabled"})
	accounts.Indexes = append(accounts.Indexes, "CREATE UNIQUE INDEX ledger_accounts_account_id ON ledger_accounts (account_id)")
	customer := core.NewAuthCollection("customer_keys", "customerkeys001")
	customer.AuthAlert.Enabled = false
	customer.PasswordAuth.Enabled = true
	customer.PasswordAuth.IdentityFields = []string{"key_lookup"}
	customer.AuthToken.Duration = 43200
	customer.Fields.GetByName("email").(*core.EmailField).Required = false
	customer.Fields.Add(&core.TextField{Name: "key_lookup", Hidden: true, Required: true, Min: 67, Max: 67, Pattern: `^v1_[0-9a-f]{64}$`})
	customer.Fields.Add(&core.TextField{Name: "account_id", Required: true, Min: 1, Max: 128})
	customer.Indexes = append(customer.Indexes, "CREATE UNIQUE INDEX customer_key_lookup ON customer_keys (key_lookup)")
	if app.Save(accounts) != nil || app.Save(customer) != nil {
		t.Fatal("synthetic predecessor metadata unavailable")
	}
	ddl, err := os.ReadFile("../../internal/ledger/testdata/split_schema_v2.sql")
	if err != nil {
		t.Fatal("pinned predecessor fixture unavailable")
	}
	if _, err := app.DB().NewQuery("DROP TABLE ledger_accounts;" + string(ddl)).Execute(); err != nil {
		t.Fatal("synthetic predecessor DDL unavailable")
	}
	if _, err := app.DB().NewQuery("INSERT INTO ledger_accounts(id,account_id,balance,enabled) VALUES('aaaaaaaaaaaaaaa','HOSTILE_PRIVATE_SENTINEL','99',1)").Execute(); err != nil {
		t.Fatal("synthetic predecessor row unavailable")
	}
	if mutate != nil {
		mutate(app)
	}
	if app.ClearBootstrap() != nil {
		t.Fatal("synthetic predecessor closure unavailable")
	}
	return dir
}

func replaceSnapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[strings.TrimPrefix(path, dir)] = sha256.Sum256(data)
		}
		return nil
	}); err != nil {
		t.Fatal("synthetic snapshot unavailable")
	}
	return out
}

func TestLedgerReplaceV2AdmissionReadOnlyAndNormalOpenRefuses(t *testing.T) {
	dir := replaceV2Fixture(t, func(app core.App) {
		ordinal := 0
		err := validateReplaceV2(func(sql string, params dbx.Params) *dbx.Query {
			ordinal++
			return app.DB().NewQuery(sql).Bind(params)
		})
		if err != nil {
			t.Fatalf("predecessor validation query ordinal %d refused", ordinal)
		}
	})
	before := replaceSnapshot(t, dir)
	tree, err := openReplaceTree(context.Background(), dir)
	if err != nil {
		t.Fatal("clean predecessor tree refused")
	}
	if tree.root.Close() != nil {
		t.Fatal("fixture tree closure unavailable")
	}
	if err := inspectReplaceV2(context.Background(), dir, nil); err != nil {
		t.Fatalf("exact predecessor admission refused: %s", ledger.Failure(err).Code)
	}
	if !reflect.DeepEqual(before, replaceSnapshot(t, dir)) {
		t.Fatal("read-only admission altered predecessor files")
	}
	s, err := ledger.Open(context.Background(), dir)
	if s != nil {
		s.Close()
		t.Fatal("ordinary V3 open accepted predecessor")
	}
	if err == nil {
		t.Fatal("ordinary open accepted predecessor")
	}
	// Existing ordinary Open uses mode=ro without immutable; SQLite may add
	// WAL shared-memory sidecars. This packet does not change that path.
	after := replaceSnapshot(t, dir)
	for _, name := range []string{"/data.db", "/auxiliary.db"} {
		if before[name] != after[name] {
			t.Fatal("ordinary refusal modified database bytes")
		}
	}
	fixture, err := os.ReadFile("../../internal/ledger/testdata/split_schema_v2.sql")
	if err != nil {
		t.Fatal("historic fixture unavailable")
	}
	start := strings.Index(string(fixture), "CREATE TABLE")
	end := strings.Index(string(fixture), "INSERT INTO")
	if strings.TrimSpace(string(fixture)[start:end]) != strings.TrimSpace(replaceSplitV2DDL) {
		t.Fatal("production admission differs from accepted predecessor DDL")
	}
}

func TestLedgerReplaceSuccessFreshV3NativeOperatorZeroAccountsAndReplayRefused(t *testing.T) {
	dir := replaceV2Fixture(t, func(app core.App) {
		// Whole-state replacement includes unrelated first-party application
		// contents inside this database, not unrelated siblings outside it.
		notes := core.NewBaseCollection("operator_notes", "operatornotes01")
		notes.Fields.Add(&core.TextField{Name: "body"})
		if app.Save(notes) != nil {
			t.Fatal("synthetic native collection unavailable")
		}
		note := core.NewRecord(notes)
		note.Set("body", "synthetic prior operator note")
		if app.Save(note) != nil {
			t.Fatal("synthetic native record unavailable")
		}
		if _, err := app.AuxDB().NewQuery("CREATE TABLE operator_notes(body TEXT)").Execute(); err != nil {
			t.Fatal("synthetic auxiliary application schema unavailable")
		}
	})
	parent := filepath.Dir(dir)
	for _, name := range []string{"lost+found", "sibling"} {
		if os.Mkdir(filepath.Join(parent, name), 0700) != nil || os.WriteFile(filepath.Join(parent, name, "preserve"), []byte("synthetic sibling"), 0600) != nil {
			t.Fatal("sibling fixture unavailable")
		}
	}
	if os.Mkdir(filepath.Join(dir, "storage"), 0700) != nil || os.WriteFile(filepath.Join(dir, "storage", "discard"), []byte("synthetic old file"), 0600) != nil {
		t.Fatal("old file fixture unavailable")
	}
	parentInfo, _ := os.Stat(parent)
	err := replaceLedgerState(context.Background(), dir, replaceCredentials(), nil)
	if err != nil {
		t.Fatalf("fresh replacement failed: %s %s", ledger.Failure(err).Code, ledger.Failure(err).Constraint)
	}
	for _, name := range []string{"lost+found", "sibling"} {
		got, err := os.ReadFile(filepath.Join(parent, name, "preserve"))
		if err != nil || string(got) != "synthetic sibling" {
			t.Fatal("replacement changed a sibling")
		}
	}
	afterParent, _ := os.Stat(parent)
	if !os.SameFile(parentInfo, afterParent) {
		t.Fatal("replacement removed the parent")
	}
	if _, err := os.Stat(filepath.Join(dir, "storage", "discard")); !os.IsNotExist(err) {
		t.Fatal("old child contents retained")
	}
	s, err := ledger.Open(context.Background(), dir)
	if err != nil {
		t.Fatal("fresh V3 open unavailable")
	}
	app, err := s.OperatorApp()
	if err != nil {
		t.Fatal("fresh native operator app unavailable")
	}
	user, err := app.FindAuthRecordByEmail(core.CollectionNameSuperusers, replaceCredentials().Email)
	if err != nil || !user.ValidatePassword(replaceCredentials().Password) {
		t.Fatal("fresh SDK native operator authentication unavailable")
	}
	for _, table := range []string{"accounts", "ledger_reservations", "ledger_credits"} {
		var count int
		if app.DB().NewQuery("SELECT count(*) FROM "+table).Row(&count) != nil || count != 0 {
			t.Fatal("fresh financial state is not empty")
		}
	}
	for _, db := range []dbx.Builder{app.DB(), app.AuxDB()} {
		var count int
		if db.NewQuery("SELECT count(*) FROM sqlite_schema WHERE name='operator_notes'").Row(&count) != nil || count != 0 {
			t.Fatal("approved whole-state replacement retained old application contents")
		}
	}
	if _, err := newWebsiteHandler(s, http.NotFoundHandler(), nil); err != nil {
		t.Fatal("fresh website initialization unavailable")
	}
	if s.Close() != nil {
		t.Fatal("fresh state closure unavailable")
	}
	before := replaceSnapshot(t, dir)
	assertReplaceFailure(t, replaceLedgerState(context.Background(), dir, replaceCredentials(), nil), "LEDGER_REPLACE_LAYOUT_REFUSED", "old_state_not_discarded")
	if !reflect.DeepEqual(before, replaceSnapshot(t, dir)) {
		t.Fatal("replayed replacement modified V3")
	}
}

func TestLedgerReplaceLayoutNegativesHaveNoEffect(t *testing.T) {
	for _, sql := range []string{
		"DROP INDEX ledger_reservations_expiry",
		"DROP INDEX ledger_reservations_expiry; CREATE INDEX ledger_reservations_expiry ON ledger_reservations(expires_at,state)",
		"CREATE TABLE accounts(id TEXT)",
		"DELETE FROM ledger_meta",
		"DELETE FROM _collections WHERE name='customer_keys'",
		"UPDATE _collections SET name='collision' WHERE name='customer_keys'",
		"UPDATE _collections SET fields='[]' WHERE name='ledger_accounts'",
		"UPDATE _collections SET fields='[{\"name\":\"HOSTILE_PRIVATE_SENTINEL\",\"type\":\"text\"}]' WHERE name='customer_keys'",
		"DROP INDEX customer_key_lookup",
		"ALTER TABLE customer_keys ADD COLUMN unexpected TEXT",
		"CREATE TRIGGER unexpected AFTER DELETE ON ledger_accounts BEGIN SELECT 1; END",
		"DROP TABLE ledger_meta; CREATE TABLE ledger_meta(singleton INTEGER PRIMARY KEY,schema_version INTEGER); INSERT INTO ledger_meta VALUES(1,1)",
	} {
		dir := replaceV2Fixture(t, func(app core.App) {
			if _, err := app.DB().NewQuery(sql).Execute(); err != nil {
				t.Fatal("negative schema fixture unavailable")
			}
		})
		before := replaceSnapshot(t, dir)
		err := replaceLedgerState(context.Background(), dir, replaceCredentials(), nil)
		assertReplaceFailure(t, err, "LEDGER_REPLACE_LAYOUT_REFUSED", "old_state_not_discarded")
		if !reflect.DeepEqual(before, replaceSnapshot(t, dir)) {
			t.Fatal("refused layout modified existing files")
		}
	}
}

func TestLedgerReplaceFilesystemNegativesHaveNoEffect(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "missing-data", "empty-aux", "symlink-child", "symlink-db", "symlink-descendant", "hardlink", "wal", "shm", "journal", "corrupt-aux", "many", "deep"} {
		t.Run(mode, func(t *testing.T) {
			dir := replaceV2Fixture(t, nil)
			outside := filepath.Join(filepath.Dir(dir), "outside")
			if os.WriteFile(outside, []byte("preserve"), 0600) != nil {
				t.Fatal("outside fixture unavailable")
			}
			must := func(err error) {
				if err != nil {
					t.Fatal("negative filesystem fixture unavailable")
				}
			}
			switch mode {
			case "missing":
				dir = filepath.Join(filepath.Dir(dir), "missing")
			case "empty":
				dir = filepath.Join(filepath.Dir(dir), "empty")
				must(os.Mkdir(dir, 0700))
			case "missing-data":
				must(os.Remove(filepath.Join(dir, "data.db")))
			case "empty-aux":
				must(os.WriteFile(filepath.Join(dir, "auxiliary.db"), nil, 0600))
			case "corrupt-aux":
				must(os.WriteFile(filepath.Join(dir, "auxiliary.db"), []byte("HOSTILE_PRIVATE_SENTINEL"), 0600))
			case "symlink-child":
				original := dir
				dir += "-link"
				must(os.Symlink(original, dir))
			case "symlink-db":
				must(os.Remove(filepath.Join(dir, "data.db")))
				must(os.Symlink(outside, filepath.Join(dir, "data.db")))
			case "symlink-descendant":
				must(os.Symlink(outside, filepath.Join(dir, "link")))
			case "hardlink":
				must(os.Link(outside, filepath.Join(dir, "link")))
			case "wal", "shm", "journal":
				must(os.WriteFile(filepath.Join(dir, "data.db-"+mode), []byte("HOSTILE_PRIVATE_SENTINEL"), 0600))
			case "many":
				for i := range replaceMaxEntries {
					must(os.WriteFile(filepath.Join(dir, "entry-"+strconv.Itoa(i)), nil, 0600))
				}
			case "deep":
				path := dir
				for range replaceMaxDepth + 1 {
					path = filepath.Join(path, "d")
					must(os.Mkdir(path, 0700))
				}
			}
			err := replaceLedgerState(context.Background(), dir, replaceCredentials(), nil)
			code := ledger.Code("LEDGER_REPLACE_TREE_REFUSED")
			if mode == "corrupt-aux" {
				code = "LEDGER_REPLACE_LAYOUT_REFUSED"
			}
			assertReplaceFailure(t, err, code, "old_state_not_discarded")
			got, err := os.ReadFile(outside)
			if err != nil || string(got) != "preserve" {
				t.Fatal("refusal changed outside file")
			}
		})
	}
}

func TestLedgerReplaceCancellationPartialDiscardAndInitFaultNeverRetry(t *testing.T) {
	for _, mode := range []string{"before", "after-admission", "first-unlink", "partial", "discard-panic", "after-discard", "init-failure", "init-panic"} {
		t.Run(mode, func(t *testing.T) {
			dir := replaceV2Fixture(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "before" {
				cancel()
			}
			o := &runtimeObserver{}
			removes, inits := 0, 0
			err := replaceLedgerStateUsing(ctx, dir, replaceCredentials(), o,
				func(ctx context.Context, dir string, observer failureowner.Observer) error {
					err := inspectReplaceV2(ctx, dir, observer)
					if mode == "after-admission" {
						cancel()
					}
					return err
				}, func(ctx context.Context, tree *replaceTree, p *replaceProgress) error {
					err := discardReplaceTreeUsing(ctx, tree, p, func(name string) error {
						removes++
						if removes == 1 && name != "data.db" {
							t.Fatal("admission-bearing data was not removed first")
						}
						if mode == "first-unlink" {
							return errors.New("HOSTILE_PRIVATE_SENTINEL")
						}
						if mode == "discard-panic" {
							panic("HOSTILE_PRIVATE_SENTINEL")
						}
						err := tree.root.Remove(name)
						if mode == "partial" {
							cancel()
						}
						return err
					})
					if mode == "after-discard" {
						cancel()
					}
					return err
				}, func(ctx context.Context, dir string, _ ledger.OperatorCredentials, observer failureowner.Observer) error {
					inits++
					if os.WriteFile(filepath.Join(dir, "partial-new"), []byte("preserve new partial state"), 0600) != nil {
						t.Fatal("partial-init fixture unavailable")
					}
					if mode == "init-panic" {
						panic("HOSTILE_PRIVATE_SENTINEL")
					}
					failureowner.Observe(observer, failuretype.InitializationUnknown)
					return errors.New("HOSTILE_PRIVATE_SENTINEL")
				})
			if err == nil {
				t.Fatal("injected fault accepted")
			}
			outcome := "discard_outcome_unknown"
			if mode == "before" || mode == "after-admission" {
				outcome = "old_state_not_discarded"
			}
			if mode == "after-discard" || strings.HasPrefix(mode, "init-") {
				outcome = "old_data_discarded_new_initialization_unconfirmed"
			}
			assertReplaceFailure(t, err, ledger.Failure(err).Code, outcome)
			if len(o.kinds) != 1 {
				t.Fatal("replacement failure recounted or lost")
			}
			if mode == "first-unlink" || mode == "partial" || mode == "discard-panic" {
				if removes != 1 {
					t.Fatal("failed discard retried")
				}
			}
			if strings.HasPrefix(mode, "init-") {
				if inits != 1 {
					t.Fatal("initialization retried")
				}
				if _, err := os.Stat(filepath.Join(dir, "partial-new")); err != nil {
					t.Fatal("failed initialization cleaned new partial state")
				}
			} else if inits != 0 {
				t.Fatal("initialization ran after cancellation or discard fault")
			}
			if mode == "partial" {
				assertReplaceFailure(t, replaceLedgerState(context.Background(), dir, replaceCredentials(), nil), "LEDGER_REPLACE_TREE_REFUSED", "old_state_not_discarded")
			}
		})
	}
}

func TestLedgerReplaceCloseFailurePreservesPrimaryAndOutcome(t *testing.T) {
	for _, withPrimary := range []bool{false, true} {
		p := &replaceProgress{stage: "replacement_initialize", started: true, discarded: true}
		var result error
		owned := false
		o := &runtimeObserver{}
		if withPrimary {
			result = replaceFailure(context.Canceled, p, "LEDGER_REPLACE_CANCELLED", "context_available", failuretype.Cancelled)
		}
		closeReplaceResource(func() error { return errors.New("HOSTILE_PRIVATE_SENTINEL") }, p, o, &result, &owned)
		code := ledger.Code("LEDGER_REPLACE_CLOSE_UNCONFIRMED")
		if withPrimary {
			code = "LEDGER_REPLACE_CANCELLED"
		}
		assertReplaceFailure(t, result, code, "old_data_discarded_new_initialization_unconfirmed")
		o.want(t, failuretype.StorageCloseUnknown)
		if owned == withPrimary {
			t.Fatal("closure ownership differs")
		}
	}
}

func TestLedgerReplaceCompiledCLIRefusalsAndUnselectedCommands(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "intake")
	build := exec.Command("go", "build", "-o", binary, ".")
	if build.Run() != nil {
		t.Fatal("replacement CLI build unavailable")
	}
	cases := [][]string{{"ledger-replace"}, append([]string{"ledger-replace"}, replaceArgs()...)}
	for _, command := range []string{"init", "serve", "website-init", "synthetic-serve", "synthetic-payments-init", "synthetic-healthcheck", "website-local", "stripe-sandbox-local", "stripe-sandbox-dev"} {
		cases = append(cases, []string{command, "--discard-existing-ledger"})
	}
	for _, args := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, binary, args...)
		// Entirely synthetic environment; no inherited private runtime inputs.
		cmd.Env = []string{"OTEL_SDK_DISABLED=true", "LEDGER_OPERATOR_EMAIL=HOSTILE_PRIVATE_SENTINEL", "LEDGER_OPERATOR_PASSWORD=HOSTILE_PRIVATE_SENTINEL", "LEDGER_VOLUME_KEY_B64=HOSTILE_PRIVATE_SENTINEL", "LEDGER_MANAGED_STARTUP_DIAGNOSTICS=true"}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		cancel()
		if err == nil || ctx.Err() == context.DeadlineExceeded || stdout.Len() != 0 {
			t.Fatal("invalid compiled invocation succeeded, emitted success or entered diagnostic hold")
		}
		var d ledger.Diagnostic
		if json.Unmarshal(stderr.Bytes(), &d) != nil || d.Code == "" || bytes.Contains(stderr.Bytes(), []byte("HOSTILE_PRIVATE_SENTINEL")) {
			t.Fatal("compiled refusal was not closed/private")
		}
		if args[0] == "ledger-replace" && len(args) > 1 && d.Code != "LEDGER_REPLACE_OPERATOR_INVALID" {
			t.Fatal("compiled replacement reached volume before credential validation")
		}
	}
}
