// Package website adapts bounded ELPS presentation to Intake's HTTP and auth contracts.
package website

import (
	"context"
	"strings"

	"possums-intake/internal/failuretype"
	"possums-intake/internal/ledger"
	"possums-intake/pkg/lispsite"
)

type Bundle = lispsite.Bundle
type Route = lispsite.Route
type Option = lispsite.Option
type Style = lispsite.Style
type Node = lispsite.Node

// Revision retains Intake rendering and diagnostics; the embedded revision owns
// preparation and isolated evaluation. Do not mutate metadata while serving.
type Revision struct{ *lispsite.Revision }

const maxNodes = lispsite.MaxNodes
const maxTextBytes = lispsite.MaxTextBytes

func intakePolicy() lispsite.Policy {
	return lispsite.Policy{
		ReservedPaths:    []string{"/healthz", "/accounts/provision", "/credits", "/reservations", "/balance", "/attestation"},
		ReservedPrefixes: []string{"/api", "/_"},
		AllowedActions:   []lispsite.Action{{Name: "auth/login", Form: "login-form"}, {Name: "auth/logout", Form: "post-form"}},
	}
}

func reserved(path string) bool {
	p := intakePolicy()
	for _, prefix := range p.ReservedPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	for _, root := range p.ReservedPaths {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func Prepare(ctx context.Context, files Bundle) (*Revision, error) {
	r, err := lispsite.Prepare(ctx, files, intakePolicy())
	if err != nil {
		if d, ok := coreDiagnostic(err); ok {
			return nil, d
		}
		return nil, fault("SITE_BUNDLE_UNAVAILABLE", "bundle_prepare", "preparation_cause_unknown", "Inspect the trusted candidate privately; active code is unchanged.", failuretype.RuntimeSetupUnknown)
	}
	// Only Intake, not the library, requires three pages and two auth actions.
	gets, login, logout := 0, 0, 0
	for _, route := range r.Routes {
		if route.Method == "GET" {
			gets++
		} else if route.Action == "auth/login" {
			login++
		} else if route.Action == "auth/logout" {
			logout++
		}
	}
	if len(r.Routes) != 5 || gets != 3 || login != 1 || logout != 1 {
		return nil, bundleFault()
	}
	return &Revision{r}, nil
}

func (r *Revision) Page(ctx context.Context, entry string) (Node, error) {
	return r.PageWithView(ctx, entry, nil)
}

func (r *Revision) PageWithView(ctx context.Context, entry string, view map[string]string) (Node, error) {
	if r == nil {
		return Node{}, renderFault()
	}
	page, err := r.Revision.PageWithView(ctx, entry, view)
	if err == nil {
		return page, nil
	}
	if d, ok := coreDiagnostic(err); ok {
		return Node{}, d
	}
	return Node{}, renderFault()
}

func fault(code, stage, constraint, next string, kind failuretype.Type) *ledger.Diagnostic {
	d := ledger.CanonicalFailure(&ledger.Diagnostic{}, kind)
	d.Code = ledger.Code(code)
	d.Stage = stage
	d.Constraint = constraint
	d.Outcome = "no_effect"
	d.NextStep = next
	return d
}
func bundleFault() *ledger.Diagnostic {
	return fault("SITE_BUNDLE_REFUSED", "bundle_prepare", "bounded_valid_revision", "Check the trusted bundle syntax, declarations and entry points privately; active code is unchanged.", failuretype.PolicyRefused)
}
func renderFault() *ledger.Diagnostic {
	return fault("SITE_RENDER_UNAVAILABLE", "page_evaluation", "bounded_complete_page_cause_unknown", "Reload the page; no backend action was replayed. Report only this code if the problem continues.", failuretype.ResponseEncodingUnknown)
}

// Match the complete closed tuple; never forward an error message or arbitrary
// library diagnostic fields. Observation still belongs to the existing HTTP owner.
func coreDiagnostic(cause error) (*ledger.Diagnostic, bool) {
	d, ok := cause.(*lispsite.Diagnostic)
	if !ok || d == nil || (d.Stage != "bundle_prepare" && d.Stage != "page_evaluation") {
		return nil, false
	}
	kind, valid := failuretype.PolicyRefused, false
	switch d.Code {
	case "BUNDLE_REFUSED":
		valid = d.Stage == "bundle_prepare" && d.Constraint == "bounded_valid_revision" && d.Category == lispsite.Refused
		if valid {
			return bundleFault(), true
		}
	case "BUNDLE_UNAVAILABLE":
		kind = failuretype.RuntimeSetupUnknown
		valid = d.Stage == "bundle_prepare" && d.Constraint == "preparation_cause_unknown" && d.Category == lispsite.SetupUnknown
	case "RENDER_UNAVAILABLE":
		kind = failuretype.ResponseEncodingUnknown
		valid = d.Stage == "page_evaluation" && d.Constraint == "bounded_complete_page_cause_unknown" && d.Category == lispsite.RenderUnknown
	case "PAGE_REFUSED":
		valid = d.Stage == "page_evaluation" && d.Constraint == "declared_presentation_capability" && d.Category == lispsite.Refused
	case "PAGE_VIEW_REFUSED":
		valid = d.Stage == "page_evaluation" && d.Constraint == "bounded_string_view" && d.Category == lispsite.Refused
	case "EXECUTION_LIMIT":
		valid = (d.Constraint == "step_budget" || d.Constraint == "evaluation_nesting") && d.Category == lispsite.Refused
	case "EXECUTION_INTERRUPTED":
		if d.Constraint == "context_cancelled" && d.Category == lispsite.Cancelled {
			kind, valid = failuretype.Cancelled, true
		} else if d.Constraint == "context_deadline" && d.Category == lispsite.DeadlineExceeded {
			kind, valid = failuretype.DeadlineExceeded, true
		}
	}
	if !valid {
		return nil, false
	}
	next := "Reload the page; no backend action was replayed."
	if d.Stage == "bundle_prepare" {
		next = "Inspect the trusted candidate privately; active code is unchanged."
	} else if d.Code == "RENDER_UNAVAILABLE" {
		next += " Report only this code if the problem continues."
	}
	return fault("SITE_"+d.Code, d.Stage, d.Constraint, next, kind), true
}
