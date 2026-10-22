package ledger

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"possums-intake/internal/failureowner"
)

func TestFreshV3RefusesBothSplitVersionsAndUnreleasedMergedV2BeforeBootstrap(t *testing.T) {
	for _, layout := range []string{"split_schema_v1.sql", "split_schema_v2.sql", "unreleased_merged_v2"} {
		t.Run(layout, func(t *testing.T) {
			dir := t.TempDir()
			app, err := bootstrap(dir, true)
			if err != nil {
				t.Fatal("historical synthetic fixture bootstrap unavailable")
			}
			schema := schemaV3
			if layout == "unreleased_merged_v2" {
				schema = strings.ReplaceAll(schema, "schema_version=3", "schema_version=2")
				schema = strings.ReplaceAll(schema, "VALUES(1,3,0)", "VALUES(1,2,0)")
				lines := strings.Split(schema, "\n")
				for i, line := range lines {
					if strings.HasPrefix(line, " initial_expires_at ") {
						lines[i] = ""
					}
				}
				schema = strings.Join(lines, "\n")
			} else {
				contents, err := os.ReadFile(filepath.Join("testdata", layout))
				if err != nil {
					t.Fatal("historical synthetic DDL unavailable")
				}
				schema = string(contents)
			}
			if _, err := app.DB().NewQuery(schema).Execute(); err != nil {
				app.ClearBootstrap()
				t.Fatal("historical synthetic schema creation unavailable")
			}
			if app.ClearBootstrap() != nil {
				t.Fatal("historical synthetic fixture close unavailable")
			}
			before := map[string][32]byte{}
			for _, name := range []string{"data.db", "auxiliary.db"} {
				contents, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal("historical synthetic state inspection unavailable")
				}
				before[name] = sha256.Sum256(contents)
			}
			service, err := openObserved(background, dir, nil, func(string, bool, failureowner.Observer) (*core.BaseApp, error) {
				t.Fatal("incompatible schema reached mutating SDK bootstrap")
				return nil, nil
			})
			if service != nil {
				service.Close()
				t.Fatal("historical schema admitted as merged v3")
			}
			codeIs(t, err, CodeSchema)
			codeIs(t, Init(background, dir), CodeExisting)
			for name, want := range before {
				contents, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || sha256.Sum256(contents) != want {
					t.Fatal("historical schema refusal modified stored state")
				}
			}
		})
	}
}

func TestFreshV3RenewalSQLConstraintsAndAdmissionValidation(t *testing.T) {
	s, _, now := testService(t)
	fund(t, s, "aaaaaaaaaaaaaaa", "100", now)
	r := reserve(t, s, ReserveInput{"aaaaaaaaaaaaaaa", "40", 600, key(now, 2)})
	var version int
	if err := s.app.DB().NewQuery("SELECT schema_version FROM ledger_meta").Row(&version); err != nil || version != 3 {
		t.Fatal("fresh initialization did not select schema v3")
	}
	for _, assignment := range []string{
		"initial_expires_at=NULL", "initial_expires_at=0", "initial_expires_at=10000000000000",
		"initial_expires_at=expires_at+1", "expires_at=initial_expires_at-1",
	} {
		err := s.run(background, func(tx *transaction) error {
			return tx.exec("UPDATE ledger_reservations SET "+assignment+" WHERE reservation_id={:id}", dbx.Params{"id": r.ReservationID})
		}, false)
		codeIs(t, err, CodeStorage)
	}
	// The current deadline is renewable, but original admission still must
	// satisfy the reserve key window even if both timestamps pass SQL checks.
	invalid := r
	invalid.InitialExpiresAt -= Millis(KeyWindowMS)
	codeIs(t, validateReservation(invalid), CodeCorrupt)
	invalid = r
	invalid.InitialExpiresAt = r.ExpiresAt + 1
	codeIs(t, validateReservation(invalid), CodeCorrupt)
	if out, err := s.Lookup(background, r.ReservationID); must(t, out, err) != r {
		t.Fatal("refused SQL mutation altered original hold")
	}
}
