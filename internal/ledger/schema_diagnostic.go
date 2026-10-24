package ledger

// These reasons describe observed validation conditions, never their cause or
// stored values. Unknown reasons retain the generic schema diagnostic.
type schemaReason string

const (
	schemaObjectMissing      schemaReason = "required_schema_object_present"
	schemaObjectDefinition   schemaReason = "supported_schema_object_definition"
	schemaObjectInventory    schemaReason = "supported_schema_object_inventory"
	schemaAccountMissing     schemaReason = "required_account_collection_present"
	schemaAccountMetadata    schemaReason = "compatible_account_collection_metadata"
	schemaAccountInventory   schemaReason = "supported_account_collection_inventory"
	schemaConnectionSettings schemaReason = "required_connection_configuration"
	schemaLedgerMetadata     schemaReason = "valid_ledger_metadata"
	schemaPaymentLayout      schemaReason = "complete_compatible_payment_schema"
	schemaPaymentMetadata    schemaReason = "valid_payment_metadata"
)

func schemaFailure(reason schemaReason) *Diagnostic {
	d := diagnostic(CodeSchema)
	var observed string
	switch reason {
	case schemaObjectMissing:
		observed = "A required schema object is absent."
	case schemaObjectDefinition:
		observed = "A required schema object has an incompatible definition."
	case schemaObjectInventory:
		observed = "The schema object inventory is incompatible; this check does not identify a particular missing object or historical version."
	case schemaAccountMissing:
		observed = "The required account collection metadata is absent."
	case schemaAccountMetadata:
		observed = "The account collection metadata is incompatible."
	case schemaAccountInventory:
		observed = "The account collection inventory is incompatible."
	case schemaConnectionSettings:
		observed = "A storage connection setting does not match the required configuration."
	case schemaLedgerMetadata:
		observed = "The ledger metadata row or version invariant is not satisfied."
	case schemaPaymentLayout:
		observed = "The optional payment object schema is incomplete or incompatible."
	case schemaPaymentMetadata:
		observed = "The payment metadata invariant is not satisfied."
	default:
		return d
	}
	d.Constraint = string(reason)
	d.NextStep = observed + " Underlying cause unknown. Preserve state and inspect the schema privately; no reset, automatic migration or financial restore is authorized."
	return d
}
