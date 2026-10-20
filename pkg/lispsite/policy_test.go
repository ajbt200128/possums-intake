package lispsite

import (
	"context"
	"strings"
	"testing"
)

func TestPolicyIsCopiedAndClosed(t *testing.T) {
	policy := Policy{ReservedPaths: []string{"/private"}, ReservedPrefixes: []string{"/hidden"}, AllowedActions: []Action{{"demo/send", "post-form"}}}
	a, ok := copyPolicy(policy)
	if !ok {
		t.Fatal("LISPSITE_TEST_POLICY_COPY")
	}
	policy.ReservedPaths[0] = "/changed"
	policy.ReservedPrefixes[0] = "/changed"
	policy.AllowedActions[0] = Action{"demo/other", "login-form"}
	if !a.reserved("/private") || !a.reserved("/private/child") || a.reserved("/privateish") || !a.reserved("/hiddenish") || a.reserved("/changed") || a.actions["demo/send"] != "post-form" || a.actions["demo/other"] != "" {
		t.Fatal("LISPSITE_TEST_POLICY_MUTATED")
	}
	for _, bad := range []Policy{
		{ReservedPaths: []string{"relative"}},
		{ReservedPrefixes: []string{"/bad?query"}},
		{ReservedPaths: []string{"/bad//path"}},
		{ReservedPrefixes: []string{"/" + strings.Repeat("a", 256)}},
		{ReservedPaths: make([]string, 33)},
		{ReservedPrefixes: make([]string, 33)},
		{AllowedActions: make([]Action, 33)},
		{AllowedActions: []Action{{"demo/send", "html"}}},
		{AllowedActions: []Action{{"demo/send", "post-form"}, {"demo/send", "login-form"}}},
		{AllowedActions: []Action{{"demo?send", "post-form"}}},
	} {
		if _, ok := copyPolicy(bad); ok {
			t.Fatal("LISPSITE_TEST_BAD_POLICY")
		}
		if _, err := Prepare(context.Background(), fixture(), bad); err == nil {
			t.Fatal("LISPSITE_TEST_BAD_POLICY_ADMITTED")
		}
	}
}

func TestNodeCopyIsBoundedAndClosed(t *testing.T) {
	for _, source := range []string{
		`(ui:p "not a document")`,
		`(ui:document "bad" (ui:p "not main"))`,
		`(ui:document "bad" (ui:main (ui:main)))`,
		`(ui:document "bad" (ui:main (ui:document "nested" (ui:main))))`,
		`(ui:document "bad" (ui:main (ui:link "/send" "not GET")))`,
		`(ui:document "bad" (ui:main (ui:login-form 'front)))`,
		`(ui:document "bad" (ui:main (ui:post-form 'missing "bad")))`,
		`(ui:document "bad" (ui:main '("raw-html" "bad")))`,
		`(ui:document "bad" (ui:main (ui:face 'unapproved "bad")))`,
		`(ui:document "bad" (ui:main ` + strings.Repeat("(ui:group ", 18) + `(ui:p "bad")` + strings.Repeat(")", 18) + `))`,
	} {
		b := fixture()
		b["pages.el"] += `(defun front (v p) ` + source + `)`
		if _, err := prepare(context.Background(), b); err == nil {
			t.Fatal("LISPSITE_TEST_UNSAFE_NODE")
		}
	}
}
