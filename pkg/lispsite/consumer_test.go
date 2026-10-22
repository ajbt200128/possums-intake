package lispsite_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"possums-intake/pkg/lispsite"
)

func TestStandaloneSinglePage(t *testing.T) {
	r, err := lispsite.Prepare(context.Background(), lispsite.Bundle{"init.el": `
(defun welcome (view preferences) (ui:document "Example" (ui:main (ui:p "Hello"))))
(site:get 'welcome "/" 'public 'welcome)
`}, lispsite.Policy{})
	if err != nil || !r.Ready() || len(r.Routes) != 1 {
		t.Fatal("LISPSITE_TEST_STANDALONE_PREPARE")
	}
	page, err := r.Page(context.Background(), "welcome")
	if err != nil || page.Kind != "document" || page.Children[0].Children[0].Text != "Hello" {
		t.Fatal("LISPSITE_TEST_STANDALONE_PAGE")
	}
}

func TestStandaloneInertHostAndDetails(t *testing.T) {
	r, err := lispsite.Prepare(context.Background(), lispsite.Bundle{"init.el": `
(defun welcome (v p) (ui:document "Example" (ui:main (ui:details "More" (ui:host 'example)))))
(site:get 'welcome "/" 'public 'welcome)
`}, lispsite.Policy{HostComponents: []string{"example"}})
	if err != nil {
		t.Fatal("LISPSITE_TEST_CONSUMER_HOST_PREPARE")
	}
	page, err := r.Page(context.Background(), "welcome")
	if err != nil || page.Children[0].Children[0].Children[0].Kind != "host" {
		t.Fatal("LISPSITE_TEST_CONSUMER_HOST_PAGE")
	}
}

func TestDependencyBoundary(t *testing.T) {
	// Use the pinned CLI supplied in PATH by the offline acceptance command.
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("LISPSITE_TEST_DEPENDENCY_INSPECTION_UNAVAILABLE")
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "possums-intake/") && dep != "possums-intake/pkg/lispsite" || strings.Contains(dep, "pocketbase") || strings.Contains(dep, "alexedwards/scs") || strings.Contains(dep, "justinas/nosurf") {
			t.Fatal("LISPSITE_TEST_APPLICATION_DEPENDENCY")
		}
	}
	// The core's direct imports must be stdlib or the pinned ELPS packages.
	out, err = exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatal("LISPSITE_TEST_IMPORT_INSPECTION_UNAVAILABLE")
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.Contains(strings.Split(dep, "/")[0], ".") && !strings.HasPrefix(dep, "github.com/luthersystems/elps/") || strings.HasPrefix(dep, "possums-intake/") {
			t.Fatal("LISPSITE_TEST_NON_ELPS_IMPORT")
		}
	}
}
