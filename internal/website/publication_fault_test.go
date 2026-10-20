package website

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func TestPublicationCommittedBeforeSwapInterruption(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "Prior selection")
	before := p.current(t).GetString("active")
	// Selection-save success runs after commit and before the registered swap.
	// A hostile SDK extension here must not yield a guessed successful receipt.
	p.b.app.OnRecordAfterUpdateSuccess(selectionCollection).Bind(&hook.Handler[*core.RecordEvent]{Id: "interruptSwap", Priority: -999, Func: func(e *core.RecordEvent) error { panic("HOSTILE_PUBLICATION_SENTINEL") }})
	output := captureWebsiteOutput(t, func() {
		if p.patch(t, map[string]any{"draft": source("Committed interrupted"), "publish": true}) == 200 {
			t.Fatal("PUBLICATION_INTERRUPTION_SUCCESS")
		}
	})
	if output != "" {
		t.Fatal("PUBLICATION_INTERRUPTION_STDIO")
	}
	if p.current(t).GetString("active") == before || p.b.host.revision != nil {
		t.Fatal("PUBLICATION_INTERRUPTION_NOT_CLOSED")
	}
	if len(p.observer.counts()) != 1 {
		t.Fatal("PUBLICATION_FAILURE_RECOUNTED")
	}
	p.b.app.OnRecordAfterUpdateSuccess(selectionCollection).Unbind("interruptSwap")
	if p.b.host.BindPublication(p.b.app) != nil {
		t.Fatal("PUBLICATION_AUTHORITATIVE_RELOAD")
	}
	response, body := p.b.request(t, "GET", "/", nil, "")
	if response.StatusCode != 200 || !strings.Contains(body, "Committed interrupted") {
		t.Fatal("PUBLICATION_RELOAD_GUESSED_SELECTION")
	}
}

func TestPublicationStaleNativeDraftCannotRewindSelection(t *testing.T) {
	p := newPublisher(t)
	p.publish(t, "First selection")
	draft, err := p.b.app.FindRecordById(publicationCollection, publicationRecord)
	if err != nil {
		t.Fatal("PUBLICATION_STALE_DRAFT_READ")
	}
	// Stock recordUpsertModal exports the entire original record, not a field diff.
	payload := draft.FieldsData()
	payload["draft"] = source("Stale editor draft")
	p.publish(t, "Newer selection")
	active := p.current(t).GetString("active")
	if p.patch(t, payload) != 200 || p.current(t).GetString("active") != active {
		t.Fatal("PUBLICATION_STALE_DRAFT_REWOUND_SELECTION")
	}
	if InitializePublication(p.b.app) == nil || p.current(t).GetString("active") != active {
		t.Fatal("PUBLICATION_REINIT_RESET_STATE")
	}
}
