package website

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Qualifies the actual stock record PATCH finalizer, not an early model event.
func TestNativePublishTransactionOrdering(t *testing.T) {
	b := newBrowser(t)
	app := b.app
	c := core.NewBaseCollection("publish_order_probe")
	c.Fields.Add(&core.BoolField{Name: "publish"})
	if app.Save(c) != nil {
		t.Fatal("SDK_GATE_COLLECTION")
	}
	record := core.NewRecord(c)
	if app.Save(record) != nil {
		t.Fatal("SDK_GATE_RECORD")
	}
	users, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		t.Fatal("SDK_GATE_USERS")
	}
	admin := core.NewRecord(users)
	admin.SetEmail("publish-probe@example.invalid")
	admin.SetPassword("synthetic-publish-password")
	if app.Save(admin) != nil {
		t.Fatal("SDK_GATE_ADMIN")
	}
	token, err := admin.NewAuthToken()
	if err != nil {
		t.Fatal("SDK_GATE_TOKEN")
	}
	order := []string{}
	app.OnRecordUpdateRequest(c.Name).BindFunc(func(e *core.RecordRequestEvent) error {
		if e.App.IsTransactional() {
			t.Fatal("SDK_GATE_PREPARATION_INSIDE_TX")
		}
		order = append(order, "prepare")
		return e.App.RunInTransaction(func(tx core.App) error {
			e.App = tx
			tx.TxInfo().OnComplete(func(txErr error) error {
				if txErr != nil {
					t.Fatal("SDK_GATE_COMMIT")
				}
				saved, err := app.FindRecordById(c.Name, record.Id)
				if err != nil || !saved.GetBool("publish") {
					t.Fatal("SDK_GATE_NOT_COMMITTED")
				}
				order = append(order, "swap")
				return nil
			})
			return e.Next()
		})
	})
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal("SDK_GATE_ROUTER")
	}
	router.Unbind(apis.DefaultActivityLoggerMiddlewareId, apis.DefaultPanicRecoverMiddlewareId)
	router.Bind(&hook.Handler[*core.RequestEvent]{Id: "probeResponse", Priority: -9999, Func: func(e *core.RequestEvent) error { err := e.Next(); order = append(order, "returned"); return err }})
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal("SDK_GATE_MUX")
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/collections/"+c.Name+"/records/"+record.Id, strings.NewReader(`{"publish":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	writer := &publicationProbeWriter{ResponseRecorder: httptest.NewRecorder(), before: func() {
		if len(order) != 2 || order[0] != "prepare" || order[1] != "swap" {
			t.Fatal("SDK_GATE_RESPONSE_BEFORE_COMMIT_SWAP")
		}
	}}
	mux.ServeHTTP(writer, req)
	if writer.Code != 200 || strings.Join(order, ",") != "prepare,swap,returned" {
		t.Fatal("SDK_GATE_STOCK_PATCH_ORDER")
	}
}

type publicationProbeWriter struct {
	*httptest.ResponseRecorder
	before func()
}

func (w *publicationProbeWriter) WriteHeader(status int) {
	w.before()
	w.ResponseRecorder.WriteHeader(status)
}
