package lispsite

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// Synthetic presentation view. Keys/values are neutral library examples, not
// application, account or payment data.
func viewBundle() Bundle {
	b := fixture()
	b["pages.el"] += `
(defun view-page (v p) (page "View" (ui:p (concat 'string (get v "label") "|" (get v "note")))))
(defun view-write (v p)
  (let ((seen (get v "label")))
    (assoc! v "label" "synthetic-mutated")
    (page "View" (ui:p seen))))`
	return b
}

func viewRevision(t *testing.T) *Revision {
	t.Helper()
	r, err := prepare(context.Background(), viewBundle())
	if err != nil {
		t.Fatal("LISPSITE_TEST_VIEW_PREPARE")
	}
	return r
}

func viewPage(t *testing.T, r *Revision, entry string, view map[string]string) Node {
	t.Helper()
	page, err := r.PageWithView(context.Background(), entry, view)
	if err != nil {
		t.Fatal("LISPSITE_TEST_VIEW_PAGE")
	}
	return page
}

func TestLegacyPageUnchanged(t *testing.T) {
	r := viewRevision(t)
	legacy, err := r.Page(context.Background(), "front")
	if err != nil {
		t.Fatal("LISPSITE_TEST_LEGACY_PAGE")
	}
	for _, view := range []map[string]string{nil, {}} {
		page := viewPage(t, r, "front", view)
		if page.Kind != legacy.Kind || nodeText(page, "p") != nodeText(legacy, "p") || len(page.Children) != len(legacy.Children) {
			t.Fatal("LISPSITE_TEST_LEGACY_VIEW_DIVERGENCE")
		}
	}
	// The legacy page function still receives a valid empty view.
	if page := viewPage(t, r, "view-page", nil); nodeText(page, "p") != "|" {
		t.Fatal("LISPSITE_TEST_EMPTY_VIEW")
	}
}

func TestDistinctViewsOnSameRevision(t *testing.T) {
	r := viewRevision(t)
	first := viewPage(t, r, "view-page", map[string]string{"label": "alpha", "note": "one"})
	second := viewPage(t, r, "view-page", map[string]string{"label": "beta", "note": "two"})
	if nodeText(first, "p") != "alpha|one" || nodeText(second, "p") != "beta|two" {
		t.Fatal("LISPSITE_TEST_VIEW_DIVERGENCE")
	}
}

func TestConcurrentViewsAndVMIsolation(t *testing.T) {
	r := viewRevision(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			label := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}[i]
			view := map[string]string{"label": label, "note": "synthetic"}
			for range 8 {
				page, err := r.PageWithView(context.Background(), "view-page", view)
				if err != nil || nodeText(page, "p") != label+"|synthetic" {
					t.Error("LISPSITE_TEST_VIEW_VM_LEAK")
				}
			}
		})
	}
	wg.Wait()
}

func TestViewCopyIndependence(t *testing.T) {
	r := viewRevision(t)
	view := map[string]string{"label": "alpha", "note": "one"}
	page := viewPage(t, r, "view-page", view)
	// Mutating the input after the call cannot alter the copied page.
	view["label"], view["note"] = "changed", "changed"
	if nodeText(page, "p") != "alpha|one" {
		t.Fatal("LISPSITE_TEST_VIEW_RETENTION")
	}
	if nodeText(viewPage(t, r, "view-page", view), "p") != "changed|changed" {
		t.Fatal("LISPSITE_TEST_VIEW_STALE_COPY")
	}
	// A page function mutating its view cannot reach the caller's map.
	original := map[string]string{"label": "alpha", "note": "one"}
	if got := nodeText(viewPage(t, r, "view-write", original), "p"); got != "alpha" {
		t.Fatal("LISPSITE_TEST_VIEW_WRITE_PAGE")
	}
	if original["label"] != "alpha" || original["note"] != "one" || len(original) != 2 {
		t.Fatal("LISPSITE_TEST_VIEW_CALLER_MUTATION")
	}
}

func TestViewRefusals(t *testing.T) {
	r := viewRevision(t)
	oversized := strings.Repeat("x", MaxTextBytes-4)
	for _, tc := range []struct {
		name string
		view map[string]string
		echo string
	}{
		{"count", func() map[string]string {
			v := map[string]string{}
			for i := range maxViewEntries + 1 {
				v[string(rune('a'+i%26))+strings.Repeat("a", i/26)] = "synthetic"
			}
			return v
		}(), ""},
		{"empty-key", map[string]string{"": "synthetic"}, ""},
		{"uppercase-key", map[string]string{"Label": "synthetic-secret-key"}, "synthetic-secret-key"},
		{"padded-key", map[string]string{" label ": "synthetic"}, ""},
		{"trailing-key", map[string]string{"label/extra": "synthetic"}, ""},
		{"long-key", map[string]string{strings.Repeat("a", 64): "synthetic"}, strings.Repeat("a", 64)},
		{"byte-budget", map[string]string{"label": oversized + "xx"}, oversized},
		{"invalid-utf8", map[string]string{"label": "synthetic-\xff-secret"}, "synthetic-"},
	} {
		_, err := r.PageWithView(context.Background(), "view-page", tc.view)
		d, ok := err.(*Diagnostic)
		if !ok || *d != (Diagnostic{"PAGE_VIEW_REFUSED", "page_evaluation", "bounded_string_view", Refused}) {
			t.Fatalf("LISPSITE_TEST_VIEW_REFUSAL %s", tc.name)
		}
		if !strings.Contains(d.Error(), "PAGE_VIEW_REFUSED") || !strings.Contains(d.Error(), "bounded_string_view") {
			t.Fatal("LISPSITE_TEST_VIEW_REFUSAL_PROJECTION")
		}
		message := d.Error() + d.Code + d.Stage + d.Constraint
		if tc.echo != "" && strings.Contains(message, tc.echo) {
			t.Fatalf("LISPSITE_TEST_VIEW_ECHO %s", tc.name)
		}
	}
	// The exact view byte budget is admitted. The entry ignores the view because
	// copying a full-budget view and rendering its full text stay separate bounds.
	boundary := map[string]string{"label": strings.Repeat("x", MaxTextBytes-len("label"))}
	if _, err := r.PageWithView(context.Background(), "front", boundary); err != nil {
		t.Fatal("LISPSITE_TEST_VIEW_BOUNDARY")
	}
	if _, err := r.PageWithView(context.Background(), "front", map[string]string{"label": boundary["label"] + "x"}); err == nil {
		t.Fatal("LISPSITE_TEST_VIEW_BOUNDARY_REFUSAL")
	}
	if _, err := r.PageWithView(context.Background(), "view-page", nil); err != nil {
		t.Fatal("LISPSITE_TEST_VIEW_REFUSAL_CONTAMINATION")
	}
}
