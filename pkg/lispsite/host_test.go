package lispsite

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func hostBundle(body string) Bundle {
	return Bundle{"init.el": `(defun welcome (v p) (ui:document "Example" (ui:main ` + body + `)))
(site:get 'welcome "/" 'public 'welcome)`}
}

func TestHostPolicyClosedAndCopied(t *testing.T) {
	ctx := context.Background()
	policy := Policy{HostComponents: []string{"example"}}
	r, err := Prepare(ctx, hostBundle(`(ui:host 'example)`), policy)
	if err != nil {
		t.Fatal("LISPSITE_TEST_HOST_PREPARE")
	}
	policy.HostComponents[0] = "changed"
	page, err := r.Page(ctx, "welcome")
	if err != nil {
		t.Fatal("LISPSITE_TEST_HOST_POLICY_MUTATION")
	}
	leaf := page.Children[0].Children[0]
	if leaf.Kind != "host" || leaf.Target != "example" || leaf.Text != "" || len(leaf.Children) != 0 {
		t.Fatal("LISPSITE_TEST_HOST_NOT_INERT_LEAF")
	}
	for _, p := range []Policy{
		{}, {HostComponents: []string{"other"}},
		{HostComponents: []string{"example", "example"}},
		{HostComponents: []string{""}}, {HostComponents: []string{"Example"}},
		{HostComponents: []string{"example/action"}},
		{HostComponents: []string{strings.Repeat("a", 49)}},
		{HostComponents: make([]string, 33)},
	} {
		if _, err := Prepare(ctx, hostBundle(`(ui:host 'example)`), p); err == nil {
			t.Fatal("LISPSITE_TEST_HOST_POLICY_REFUSAL")
		}
	}
	names := make([]string, 32)
	for i := range names {
		names[i] = fmt.Sprintf("slot-%d", i)
	}
	if _, err := Prepare(ctx, hostBundle(`(ui:host 'slot-31)`), Policy{HostComponents: names}); err != nil {
		t.Fatal("LISPSITE_TEST_HOST_POLICY_BOUNDARY")
	}
}

func TestHostAndDetailsNodeValidation(t *testing.T) {
	ctx := context.Background()
	policy := Policy{HostComponents: []string{"example"}}
	for _, body := range []string{
		`(ui:host 'unknown)`, `(ui:host "example")`,
		`'( "host" "unknown")`, `'("host" "example" "extra")`,
		`'("host" 1)`, `(ui:details 'symbol (ui:p "body"))`,
		`(ui:details "Summary" (ui:main))`,
		`(ui:details "Summary" (ui:document "nested" (ui:main)))`,
		`'("details" "Summary")`, `'("details" "Summary" ("p" "one") ("p" "two"))`,
		strings.Repeat(`(ui:details "Summary" `, 18) + `(ui:p "body")` + strings.Repeat(")", 18),
		`(let ((s "x")) (dotimes (i 15) (set! s (concat 'string s s))) (ui:details s (ui:p "body")))`,
	} {
		if _, err := Prepare(ctx, hostBundle(body), policy); err == nil {
			t.Fatal("LISPSITE_TEST_HOST_DETAILS_NODE_REFUSAL")
		}
		// Named functions not registered as routes are checked again on copying.
		b := hostBundle(`(ui:p "safe")`)
		b["init.el"] += `(defun later (v p) (ui:document "Example" (ui:main ` + body + `)))`
		r, err := Prepare(ctx, b, policy)
		if err != nil {
			t.Fatal("LISPSITE_TEST_HOST_DETAILS_LATE_PREPARE")
		}
		if _, err := r.Page(ctx, "later"); err == nil {
			t.Fatal("LISPSITE_TEST_HOST_DETAILS_LATE_REFUSAL")
		}
	}
	r, err := Prepare(ctx, hostBundle(`(ui:details "Summary" (ui:host 'example))`), policy)
	if err != nil {
		t.Fatal("LISPSITE_TEST_DETAILS_PREPARE")
	}
	page, err := r.Page(ctx, "welcome")
	if err != nil {
		t.Fatal("LISPSITE_TEST_DETAILS_PAGE")
	}
	details := page.Children[0].Children[0]
	if details.Kind != "details" || details.Text != "Summary" || len(details.Children) != 1 || details.Children[0].Target != "example" {
		t.Fatal("LISPSITE_TEST_DETAILS_SHAPE")
	}
	// Even discarded preparation-time host expressions obey the allowlist.
	b := hostBundle(`(ui:p "safe")`)
	b["init.el"] += `(ui:host 'unknown)`
	if _, err := Prepare(ctx, b, policy); err == nil {
		t.Fatal("LISPSITE_TEST_HOST_PREPARATION_ALLOWLIST")
	}
}
