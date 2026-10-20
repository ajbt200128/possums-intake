package siteauth

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestCustomerSchemaRollbackAndCollision(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		f := newFixture(t)
		hook := f.app.OnCollectionCreate(CustomerCollection)
		id := hook.BindFunc(func(e *core.CollectionEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			return errors.New(hostile)
		})
		err := InitializeCustomerAuth(f.app)
		d := assertCode(t, err, "SITE_CUSTOMER_SCHEMA_UNAVAILABLE")
		encoded, _ := json.Marshal(d)
		if strings.Contains(string(encoded), hostile) {
			t.Fatal("schema exception released")
		}
		if _, err := f.app.FindCollectionByNameOrId(CustomerCollection); err == nil {
			t.Fatal("interrupted schema persisted collection")
		}
		var count int
		if f.app.DB().NewQuery("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='customer_keys'").Row(&count) != nil || count != 0 {
			t.Fatal("interrupted schema persisted table")
		}
		hook.Unbind(id)
		if InitializeCustomerAuth(f.app) != nil {
			t.Fatal("explicit initialization after verified rollback unavailable")
		}
		if _, err := f.app.FindRecordById(f.collection.Name, f.record.Id); err != nil {
			t.Fatal("schema initialization lost existing SDK state")
		}
	})
	t.Run("collision", func(t *testing.T) {
		f := newFixture(t)
		c := core.NewBaseCollection("synthetic_collision", CustomerCollectionID)
		if f.app.Save(c) != nil {
			t.Fatal("synthetic collision unavailable")
		}
		assertCode(t, InitializeCustomerAuth(f.app), "SITE_CUSTOMER_SCHEMA_REFUSED")
		if _, err := f.app.FindCollectionByNameOrId(CustomerCollection); err == nil {
			t.Fatal("collision silently initialized customer schema")
		}
		if current, err := f.app.FindCollectionByNameOrId(c.Id); err != nil || current.Name != c.Name || !current.IsBase() {
			t.Fatal("colliding schema overwritten")
		}
	})
}
