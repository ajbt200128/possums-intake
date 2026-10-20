package website

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
)

const publicationCollection = "website_bundles"
const publicationRecord = "websitebundle01"
const selectionCollection = "website_selection"
const publicationHost = 1
const maxStoredBundle = 256 << 10

type storedBundle struct {
	Host  int    `json:"host"`
	Files Bundle `json:"files"`
}
type revisionKey struct{}

func requestRevision(r interface{ Context() context.Context }) *Revision {
	revision, _ := r.Context().Value(revisionKey{}).(*Revision)
	return revision
}

// InitializePublication creates only the empty code inventory. It is explicit:
// ordinary serve and website-local never call it, and no demo is made active.
// Currently qualified only with isolated synthetic SDK fixtures.
func InitializePublication(app core.App) (result error) {
	defer func() {
		if recover() != nil || result != nil {
			result = publicationFault("publication_schema")
		}
	}()
	if app.IsTransactional() {
		return publicationFault("publication_schema")
	}
	return app.RunInTransaction(initializePublication)
}

func initializePublication(tx core.App) error {
	c := core.NewBaseCollection(publicationCollection)
	c.Fields.Add(&core.JSONField{Name: "draft", MaxSize: maxStoredBundle})
	c.Fields.Add(&core.BoolField{Name: "publish"})
	if err := tx.Save(c); err != nil {
		return publicationFault("publication_schema")
	}
	r := core.NewRecord(c)
	r.Id = publicationRecord
	if err := tx.Save(r); err != nil {
		return publicationFault("publication_schema")
	}
	selection := core.NewBaseCollection(selectionCollection)
	for _, name := range []string{"active", "previous"} {
		selection.Fields.Add(&core.JSONField{Name: name, MaxSize: maxStoredBundle})
	}
	if err := tx.Save(selection); err != nil {
		return publicationFault("publication_schema")
	}
	r = core.NewRecord(selection)
	r.Id = publicationRecord
	return tx.Save(r)
}

func prepareStored(ctx context.Context, data string) (*Revision, []byte, error) {
	if len(data) > maxStoredBundle {
		return nil, nil, bundleFault()
	}
	var bundle storedBundle
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || bundle.Host != publicationHost {
		return nil, nil, bundleFault()
	}
	// Prepare captures once; serialization below uses the same private decoded map.
	revision, err := Prepare(ctx, bundle.Files)
	if err != nil {
		return nil, nil, err
	}
	for _, route := range revision.Routes {
		if route.Method != "GET" {
			continue
		}
		page, err := revision.Page(ctx, route.Entry)
		if err != nil {
			return nil, nil, err
		}
		if _, err = revision.Render(page, "synthetic-preflight", ""); err != nil {
			return nil, nil, err
		}
	}
	captured, err := json.Marshal(bundle)
	if err != nil || len(captured) > maxStoredBundle {
		return nil, nil, bundleFault()
	}
	return revision, captured, nil
}

// BindPublication installs the stock native record-save publication operation
// and cold-loads only the persisted active code. A load error leaves admission
// closed; native routes still work so an explicit publish can repair code state.
// No schema creation, credential mutation, retry or fallback occurs here.
func (h *Host) BindPublication(app core.App) (result error) {
	defer func() {
		if recover() != nil {
			result = publicationFault("publication_load")
		}
	}()
	app.OnRecordUpdateRequest(publicationCollection).Bind(&hook.Handler[*core.RecordRequestEvent]{
		Id: "intakeWebsitePublication", Func: func(e *core.RecordRequestEvent) (result error) {
			confirmed := false
			defer func() {
				if recover() != nil {
					result = publicationFault("publication_dispatch")
					if confirmed {
						result = publicationCompletionFault()
					}
				}
			}()
			info, err := e.RequestInfo()
			if err != nil {
				return publicationFault("publication_admission")
			}
			if e.Record.Id != publicationRecord || info.Body["publish"] != true {
				return e.Next()
			}
			// Batch requests already hold a transaction. They cannot satisfy prepare-first.
			if e.App.IsTransactional() {
				return publicationFault("publication_admission")
			}
			h.publish.Lock()
			defer h.publish.Unlock()
			revision, captured, err := prepareStored(e.Request.Context(), e.Record.GetString("draft"))
			if err != nil {
				return fault("SITE_PUBLICATION_REFUSED", "publication_prepare", "bounded_renderable_code", "Check the trusted draft privately; active code is unchanged.", ledger.FailureKind(err, failuretype.RuntimeSetupUnknown))
			}
			h.admission.Lock()
			defer h.admission.Unlock()
			oldCSS := ""
			if h.revision != nil {
				oldCSS = h.revision.CSS()
			}
			completed := false
			// Any panic or unobserved completion leaves new admission closed. Previously
			// captured requests keep their ordinary references and can finish.
			h.revision = nil
			defer func() {
				if !completed {
					h.revision = nil
				}
			}()
			err = e.App.RunInTransaction(func(tx core.App) error {
				current, err := tx.FindRecordById(selectionCollection, publicationRecord)
				if err != nil {
					return publicationFault("publication_commit")
				}
				// Selection lives separately so an ordinary stale stock draft form
				// cannot overwrite publication metadata with its whole-record payload.
				current.Set("previous", current.GetRaw("active"))
				current.Set("active", json.RawMessage(captured))
				if err := tx.Save(current); err != nil {
					return publicationFault("publication_commit")
				}
				e.Record.Set("publish", false)
				e.App = tx
				tx.TxInfo().OnComplete(func(txErr error) error {
					completed = true
					if txErr != nil {
						// A commit error is not evidence of rollback. Stay closed until an
						// explicit publish/restart authoritatively reloads the selection.
						return nil
					}
					h.previousCSS = oldCSS
					h.revision = revision
					confirmed = true
					return nil
				})
				return e.Next()
			})
			if err != nil {
				d := publicationFault("publication_commit")
				if confirmed {
					d = publicationCompletionFault()
				}
				return d
			}
			if !completed || h.revision != revision {
				return publicationFault("publication_swap")
			}
			return nil
		},
	})
	h.publish.Lock()
	defer h.publish.Unlock()
	h.admission.Lock()
	defer h.admission.Unlock()
	h.revision, h.previousCSS = nil, ""
	if app.IsTransactional() {
		return publicationFault("publication_load")
	}
	inventory, inventoryErr := app.FindCollectionByNameOrId(publicationCollection)
	selection, selectionErr := app.FindCollectionByNameOrId(selectionCollection)
	if inventoryErr != nil || selectionErr != nil || !publicationSchema(inventory, selection) {
		return publicationFault("publication_load")
	}
	if _, err := app.FindRecordById(publicationCollection, publicationRecord); err != nil {
		return publicationFault("publication_load")
	}
	record, err := app.FindRecordById(selectionCollection, publicationRecord)
	if err != nil {
		return publicationFault("publication_load")
	}
	revision, _, err := prepareStored(context.Background(), record.GetString("active"))
	if err != nil {
		return publicationFault("publication_load")
	}
	// Unsupported/corrupt retained assets never substitute different bytes.
	previous, _, _ := prepareStored(context.Background(), record.GetString("previous"))
	if previous != nil {
		h.previousCSS = previous.CSS()
	}
	h.revision = revision
	return nil
}

func publicationCompletionFault() *ledger.Diagnostic {
	d := publicationFault("publication_completion")
	d.Outcome = "website_selection_committed_response_unconfirmed"
	d.NextStep = "Reload the website to observe selected code; do not replay publication based on this response failure."
	return d
}

func publicationFault(stage string) *ledger.Diagnostic {
	d := fault("SITE_PUBLICATION_UNAVAILABLE", stage, "authoritative_code_selection_cause_unknown", "Inspect code selection privately; explicitly publish valid code or restart. No operation was replayed; publication success is unconfirmed.", failuretype.NativeWriteUnknown)
	if stage == "publication_load" || stage == "website_admission" {
		d = fault("SITE_PUBLICATION_UNAVAILABLE", stage, "authoritative_code_selection_cause_unknown", "Explicitly publish valid code or restart after checking selected code privately; no fallback or replay occurred.", failuretype.NativeReadUnknown)
	}
	d.Outcome = "website_selection_unconfirmed"
	if stage == "publication_admission" {
		d.Outcome = "no_effect"
	}
	return d
}
