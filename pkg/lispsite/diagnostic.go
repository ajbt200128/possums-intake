package lispsite

import "fmt"

// Category is a closed, content-free failure class. Diagnostics are transient;
// the library neither records them nor observes or retries backend operations.
type Category string

const (
	Refused          Category = "refused"
	SetupUnknown     Category = "setup_unknown"
	RenderUnknown    Category = "render_unknown"
	Cancelled        Category = "cancelled"
	DeadlineExceeded Category = "deadline_exceeded"
)

// Diagnostic contains only locally authored evidence, never SDK exceptions,
// source text or panic payloads. Adapters must validate the complete tuple before
// mapping it to their own diagnostics, rather than forwarding arbitrary fields.
type Diagnostic struct {
	Code, Stage, Constraint string
	Category                Category
}

func fault(code, stage, constraint string, category Category) *Diagnostic {
	return &Diagnostic{Code: code, Stage: stage, Constraint: constraint, Category: category}
}

func (d *Diagnostic) Error() string {
	// A caller can construct this type. Only exact locally authored tuples may
	// contribute fields to a message; hostile or incomplete evidence is unknown.
	const unknown = "LISPSITE_DIAGNOSTIC_UNAVAILABLE — stage=diagnostic_projection; constraint=closed_evidence_cause_unknown. Inspect the site privately; no backend action was invoked by this library."
	if d == nil {
		return unknown
	}
	switch *d {
	case Diagnostic{"BUNDLE_REFUSED", "bundle_prepare", "bounded_valid_revision", Refused},
		Diagnostic{"BUNDLE_UNAVAILABLE", "bundle_prepare", "preparation_cause_unknown", SetupUnknown},
		Diagnostic{"RENDER_UNAVAILABLE", "page_evaluation", "bounded_complete_page_cause_unknown", RenderUnknown},
		Diagnostic{"PAGE_REFUSED", "page_evaluation", "declared_presentation_capability", Refused},
		Diagnostic{"PAGE_VIEW_REFUSED", "page_evaluation", "bounded_string_view", Refused},
		Diagnostic{"EXECUTION_LIMIT", "bundle_prepare", "step_budget", Refused},
		Diagnostic{"EXECUTION_LIMIT", "bundle_prepare", "evaluation_nesting", Refused},
		Diagnostic{"EXECUTION_LIMIT", "page_evaluation", "step_budget", Refused},
		Diagnostic{"EXECUTION_LIMIT", "page_evaluation", "evaluation_nesting", Refused},
		Diagnostic{"EXECUTION_INTERRUPTED", "bundle_prepare", "context_cancelled", Cancelled},
		Diagnostic{"EXECUTION_INTERRUPTED", "bundle_prepare", "context_deadline", DeadlineExceeded},
		Diagnostic{"EXECUTION_INTERRUPTED", "page_evaluation", "context_cancelled", Cancelled},
		Diagnostic{"EXECUTION_INTERRUPTED", "page_evaluation", "context_deadline", DeadlineExceeded}:
	default:
		return unknown
	}
	next := "Inspect the page entry and presentation nodes privately; no backend action was invoked by this library."
	if d.Stage == "bundle_prepare" {
		next = "Inspect the trusted bundle syntax, declarations and entry points privately; retain the active revision."
	}
	return fmt.Sprintf("%s — stage=%s; constraint=%s. %s", d.Code, d.Stage, d.Constraint, next)
}
