package website

import (
	"context"
	"encoding/json"
	"errors"
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
	custom, err := json.Marshal(source("Explicit retained custom publication"))
	if err != nil {
		t.Fatal("SITE_DEFAULT_TEST_SOURCE")
	}
	selection.Set("previous", selection.GetRaw("active"))
	selection.Set("active", json.RawMessage(custom))
	if b.app.Save(selection) != nil {
		t.Fatal("SITE_DEFAULT_TEST_SAVE")
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
