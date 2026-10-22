package siteauth

import (
	"encoding/json"
	"errors"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"testing"
)

type customerSchemaReadFault struct{ core.App }

func (a customerSchemaReadFault) FindCollectionByNameOrId(string) (*core.Collection, error) {
	return nil, errors.New(hostile)
}

func TestCustomerSchemaMissingFaultAndCollisionNeverCreate(t *testing.T) {
	f := newFixture(t)
	created := false
	f.app.OnCollectionCreate(CustomerCollection).BindFunc(func(e *core.CollectionEvent) error { created = true; return e.Next() })
	assertCode(t, InitializeCustomerAuth(f.app), "SITE_CUSTOMER_SCHEMA_REFUSED")
	d := assertCode(t, InitializeCustomerAuth(customerSchemaReadFault{f.app}), "SITE_CUSTOMER_SCHEMA_UNAVAILABLE")
	encoded, _ := json.Marshal(d)
	if strings.Contains(string(encoded), hostile) || created {
		t.Fatal("schema check exposed failure or created state")
	}
	var count int
	if f.app.DB().NewQuery("SELECT count(*) FROM sqlite_schema WHERE name='accounts'").Row(&count) != nil || count != 0 {
		t.Fatal("missing schema check created table")
	}
	c := core.NewBaseCollection("synthetic_collision", CustomerCollectionID)
	if f.app.Save(c) != nil {
		t.Fatal("synthetic collision unavailable")
	}
	assertCode(t, InitializeCustomerAuth(f.app), "SITE_CUSTOMER_SCHEMA_REFUSED")
	if current, err := f.app.FindCollectionByNameOrId(c.Id); err != nil || current.Name != c.Name || !current.IsBase() {
		t.Fatal("colliding schema overwritten")
	}
	if _, err := f.app.FindRecordById(f.collection.Name, f.record.Id); err != nil {
		t.Fatal("schema check changed existing SDK state")
	}
}
