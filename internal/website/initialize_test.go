package website

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func TestDefaultPublicationExplicitIdempotentAndPreservesCode(t *testing.T) {
	b := newBrowser(t)
	ctx := context.Background()
	if InitializeDefaultPublication(ctx, b.app) != nil || b.host.BindPublication(b.app) != nil {
		t.Fatal("SITE_DEFAULT_EXPLICIT_INITIALIZATION")
	}
	selection, err := b.app.FindRecordById(selectionCollection, publicationRecord)
	if err != nil || selection.GetString("active") == "" || selection.GetString("previous") != "null" {
		t.Fatal("SITE_DEFAULT_INITIAL_SELECTION")
	}
	inventory, err := b.app.FindRecordById(publicationCollection, publicationRecord)
	if err != nil || inventory.GetString("draft") != selection.GetString("active") || inventory.GetBool("publish") {
		t.Fatal("SITE_DEFAULT_INITIAL_DRAFT")
	}
	// An operator-owned old notice intentionally remains selected even when a
	// newer embedded default can describe live mode. Initialization is no upgrade.
	old := source("Explicit retained custom publication")
	old.Files["pages.el"] += `(defun topup-page (view preferences) (page "Old operator page" (ui:p "` + sandboxNotice + `")))`
	custom, err := json.Marshal(old)
	if err != nil {
		t.Fatal("SITE_DEFAULT_TEST_SOURCE")
	}
	selection.Set("previous", selection.GetRaw("active"))
	selection.Set("active", json.RawMessage(custom))
	if b.app.Save(selection) != nil {
		t.Fatal("SITE_DEFAULT_TEST_SAVE")
	}
	draft, err := json.Marshal(source("Distinct unfinished operator draft"))
	if err != nil {
		t.Fatal("SITE_DEFAULT_TEST_DRAFT")
	}
	inventory.Set("draft", json.RawMessage(draft))
	if b.app.Save(inventory) != nil {
		t.Fatal("SITE_DEFAULT_TEST_DRAFT_SAVE")
	}
	beforeActive, beforePrevious, beforeDraft := selection.GetString("active"), selection.GetString("previous"), inventory.GetString("draft")
	if InitializeDefaultPublication(ctx, b.app) != nil {
		t.Fatal("SITE_DEFAULT_IDEMPOTENT_INITIALIZATION")
	}
	selection, err = b.app.FindRecordById(selectionCollection, publicationRecord)
	if err != nil || selection.GetString("active") != beforeActive || selection.GetString("previous") != beforePrevious {
		t.Fatal("SITE_DEFAULT_OVERWROTE_SELECTION")
	}
	inventory, err = b.app.FindRecordById(publicationCollection, publicationRecord)
	if err != nil || inventory.GetString("draft") != beforeDraft {
		t.Fatal("SITE_DEFAULT_OVERWROTE_DRAFT")
	}
	if b.host.BindPublication(b.app) != nil {
		t.Fatal("SITE_DEFAULT_RELOAD_RETAINED_PUBLICATION")
	}
	page, err := b.host.revision.PageWithView(ctx, "topup-page", map[string]string{"payment-mode": "live"})
	if err != nil {
		t.Fatal("SITE_DEFAULT_RETAINED_OLD_PAGE")
	}
	body, err := b.host.revision.Render(page, "csrf", "")
	if err != nil || !strings.Contains(string(body), "Old operator page") || !strings.Contains(string(body), sandboxNotice) || strings.Contains(string(body), "Payments are live.") {
		t.Fatal("SITE_DEFAULT_EMBEDDED_SOURCE_NOT_ACTIVE_UPGRADE")
	}
}

func TestDefaultPublicationAtomicFailureAndPartialStateRefusal(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		b := newBrowser(t)
		b.app.OnRecordCreate(selectionCollection).Bind(&hook.Handler[*core.RecordEvent]{Id: "defaultPublicationSyntheticRefusal", Func: func(e *core.RecordEvent) error { return errors.New("SYNTHETIC_PRIVATE_SENTINEL") }})
		if InitializeDefaultPublication(context.Background(), b.app) == nil {
			t.Fatal("SITE_DEFAULT_INTERRUPTED_SETUP_ACCEPTED")
		}
		var count int
		if b.app.DB().NewQuery("SELECT count(*) FROM _collections WHERE name IN ('website_bundles','website_selection')").Row(&count) != nil || count != 0 {
			t.Fatal("SITE_DEFAULT_INITIAL_TRANSACTION_NOT_ATOMIC")
		}
	})
	t.Run("partial", func(t *testing.T) {
		b := newBrowser(t)
		if b.app.Save(core.NewBaseCollection(publicationCollection)) != nil {
			t.Fatal("SITE_DEFAULT_TEST_PARTIAL_SCHEMA")
		}
		if InitializeDefaultPublication(context.Background(), b.app) == nil {
			t.Fatal("SITE_DEFAULT_PARTIAL_STATE_ACCEPTED")
		}
		if _, err := b.app.FindCollectionByNameOrId(selectionCollection); err == nil {
			t.Fatal("SITE_DEFAULT_PARTIAL_STATE_REPAIRED")
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		b := newBrowser(t)
		if InitializeDefaultPublication(context.Background(), b.app) != nil {
			t.Fatal("SITE_DEFAULT_TEST_SETUP")
		}
		selection, err := b.app.FindRecordById(selectionCollection, publicationRecord)
		if err != nil {
			t.Fatal("SITE_DEFAULT_TEST_SELECTION")
		}
		selection.Set("active", json.RawMessage(`{"host":999}`))
		if b.app.Save(selection) != nil {
			t.Fatal("SITE_DEFAULT_TEST_CORRUPTION")
		}
		if InitializeDefaultPublication(context.Background(), b.app) == nil || b.host.BindPublication(b.app) == nil {
			t.Fatal("SITE_DEFAULT_CORRUPT_SELECTION_ACCEPTED")
		}
		selected, err := b.app.FindRecordById(selectionCollection, publicationRecord)
		if err != nil || selected.GetString("active") != selection.GetString("active") {
			t.Fatal("SITE_DEFAULT_CORRUPTION_RESET")
		}
	})
}
