package lispsite

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCopiedPageTextLimitIsKnownAndContentFree(t *testing.T) {
	bundle := hostBundle(`(ui:p "safe")`)
	bundle["init.el"] += `(defun large (v p) (ui:document "Example" (ui:main (ui:text (get v "text")) (ui:text (get v "text")))))`
	r, err := Prepare(context.Background(), bundle, Policy{})
	if err != nil {
		t.Fatal("LISPSITE_TEST_PAGE_LIMIT_PREPARE")
	}
	_, err = r.PageWithView(context.Background(), "large", map[string]string{"text": strings.Repeat("PRIVATE_TEXT_SENTINEL", 900)})
	var d *Diagnostic
	if !errors.As(err, &d) || d.Code != "PAGE_TEXT_LIMIT" || d.Stage != "page_evaluation" || d.Constraint != "complete_page_text_budget" || d.Category != Refused || strings.Contains(d.Error(), "PRIVATE_TEXT_SENTINEL") || strings.Contains(d.Error(), "DIAGNOSTIC_UNAVAILABLE") {
		t.Fatal("LISPSITE_TEST_PAGE_LIMIT_CLASSIFICATION")
	}
}
