package website

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/pocketbase/pocketbase/core"
)

// InitializeDefaultPublication explicitly installs the bundled initial website
// only when both publication collections are absent. Existing published code is
// validated and preserved; partial/incompatible state is never reset.
func InitializeDefaultPublication(ctx context.Context, app core.App) (result error) {
	defer func() {
		if recover() != nil {
			result = publicationFault("publication_schema")
		}
	}()
	if app == nil || app.IsTransactional() {
		return publicationFault("publication_schema")
	}
	bundle, err := Bundled()
	if err != nil {
		return err
	}
	data, err := json.Marshal(storedBundle{Host: publicationHost, Files: bundle})
	if err != nil {
		return publicationFault("publication_schema")
	}
	if _, _, err := prepareStored(ctx, string(data)); err != nil {
		return err
	}
	// Existing selections are evaluated before acquiring a write transaction.
	selection, selectionErr := app.FindCollectionByNameOrId(selectionCollection)
	inventory, inventoryErr := app.FindCollectionByNameOrId(publicationCollection)
	if selectionErr == nil && inventoryErr == nil {
		if !publicationSchema(inventory, selection) {
			return publicationFault("publication_schema")
		}
		record, err := app.FindRecordById(selectionCollection, publicationRecord)
		if err != nil {
			return publicationFault("publication_load")
		}
		if _, _, err := prepareStored(ctx, record.GetString("active")); err != nil {
			return publicationFault("publication_load")
		}
		if _, err := app.FindRecordById(publicationCollection, publicationRecord); err != nil {
			return publicationFault("publication_schema")
		}
		return nil
	}
	if !errors.Is(selectionErr, sql.ErrNoRows) || !errors.Is(inventoryErr, sql.ErrNoRows) {
		return publicationFault("publication_schema")
	}
	err = app.RunInTransaction(func(tx core.App) error {
		if err := initializePublication(tx); err != nil {
			return publicationFault("publication_schema")
		}
		for _, name := range []string{publicationCollection, selectionCollection} {
			record, err := tx.FindRecordById(name, publicationRecord)
			if err != nil {
				return publicationFault("publication_schema")
			}
			field := "draft"
			if name == selectionCollection {
				field = "active"
			}
			record.Set(field, json.RawMessage(data))
			if err := tx.Save(record); err != nil {
				return publicationFault("publication_schema")
			}
		}
		return nil
	})
	if err != nil {
		return publicationFault("publication_schema")
	}
	return nil
}

func publicationSchema(inventory, selection *core.Collection) bool {
	if inventory == nil || selection == nil || inventory.Type != core.CollectionTypeBase || selection.Type != core.CollectionTypeBase || len(inventory.Fields) != 3 || len(selection.Fields) != 3 {
		return false
	}
	draft, ok := inventory.Fields.GetByName("draft").(*core.JSONField)
	if !ok || draft.MaxSize != maxStoredBundle {
		return false
	}
	if _, ok := inventory.Fields.GetByName("publish").(*core.BoolField); !ok {
		return false
	}
	for _, name := range []string{"active", "previous"} {
		field, ok := selection.Fields.GetByName(name).(*core.JSONField)
		if !ok || field.MaxSize != maxStoredBundle {
			return false
		}
	}
	return true
}
