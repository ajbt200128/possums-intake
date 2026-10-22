package website

import (
	"context"
	"embed"
	"regexp"
	"strings"
	"testing"
)

//go:embed bundle/*.el
var fixtureFiles embed.FS

func Fixture() Bundle {
	files := Bundle{}
	entries, _ := fixtureFiles.ReadDir("bundle")
	for _, entry := range entries {
		b, _ := fixtureFiles.ReadFile("bundle/" + entry.Name())
		files[entry.Name()] = string(b)
	}
	return files
}
func prepared(t *testing.T) *Revision {
	t.Helper()
	r, err := Prepare(context.Background(), Fixture())
	if err != nil {
		t.Fatal("SITE_TEST_PREPARE_REFUSED")
	}
	return r
}

func syntheticNodeText(t *testing.T, page Node, kind string) string {
	t.Helper()
	var find func(Node) (string, bool)
	find = func(n Node) (string, bool) {
		if n.Kind == kind {
			return n.Text, true
		}
		for _, child := range n.Children {
			if text, ok := find(child); ok {
				return text, true
			}
		}
		return "", false
	}
	text, ok := find(page)
	if !ok {
		t.Fatalf("SITE_TEST_NODE_MISSING_%s", kind)
	}
	return text
}

var styleColor = regexp.MustCompile(`"#[0-9a-fA-F]{3,8}"`)

func TestBundleFixture(t *testing.T) {
	r := prepared(t)
	if len(r.Options) != 1 || r.Options[0].Default != true {
		t.Fatal("SITE_TEST_OPTION_MISMATCH")
	}
	for _, route := range r.Routes {
		if route.Method == "GET" {
			if _, err := r.Page(context.Background(), route.Entry); err != nil {
				t.Fatal("SITE_TEST_PAGE_REFUSED")
			}
		}
	}
}
func TestBundleRefusals(t *testing.T) {
	for _, change := range []func(Bundle){
		func(b Bundle) { b["init.el"] = `(site:load "init.el")` },
		func(b Bundle) { b["init.el"] += `(site:load "shared.el")` },
		func(b Bundle) { b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/home"`, `"/%68ome"`) },
		func(b Bundle) {
			b["routes.el"] = strings.ReplaceAll(b["routes.el"], `'signed-home "/home"`, `'login "/home"`)
		},
		func(b Bundle) {
			b["style.el"] = styleColor.ReplaceAllString(b["style.el"], `"url(https://invalid.example)"`)
		},
		func(b Bundle) {
			b["options.el"] += `(defcustom home-show-introduction false "duplicate" :type 'boolean :group 'home)`
		},
		func(b Bundle) {
			b["pages.el"] += `(defun public-home (v p) (ui:document "bad" (ui:main (ui:link "https://invalid.example" "bad"))))`
		},
		func(b Bundle) { b["pages.el"] += `(debug-print "not available")` },
		func(b Bundle) { b["routes.el"] = `(site:get 'public-home "/" 'public 'public-home)` },
		func(b Bundle) { b["routes.el"] = strings.ReplaceAll(b["routes.el"], "auth/logout", "auth/login") },
		func(b Bundle) {
			b["pages.el"] += `(defun public-home (v p) (ui:document "bad" (ui:main (ui:post-form 'sign-in "bad"))))`
		},
		func(b Bundle) {
			b["pages.el"] += `(defun public-home (v p) (ui:document "bad" (ui:main (ui:login-form 'sign-out))))`
		},
	} {
		b := Fixture()
		change(b)
		if _, err := Prepare(context.Background(), b); err == nil {
			t.Fatal("SITE_TEST_UNSAFE_BUNDLE_ADMITTED")
		}
	}
	prepared(t)
}

func TestIntakeReservedRoutes(t *testing.T) {
	for _, path := range []string{"/api", "/api/foo", "/apifoo", "/_", "/_site", "/_/x", "/healthz", "/healthz/sub", "/accounts/provision", "/accounts/provision/sub", "/credits", "/credits/sub", "/reservations", "/reservations/sub", "/balance", "/balance/sub", "/attestation", "/attestation/sub", "/topup", "/topup/start", "/topup/status", "/topup/return", "/payments", "/payments/stripe/webhook"} {
		b := Fixture()
		b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/home"`, `"`+path+`"`)
		if !reserved(path) {
			t.Fatal("SITE_TEST_RESERVED_DISPATCH")
		}
		if _, err := Prepare(context.Background(), b); err == nil {
			t.Fatal("SITE_TEST_RESERVED_ADMISSION")
		}
	}
}

func TestCapturedSourcesAndRepeatedPreparation(t *testing.T) {
	active := prepared(t)
	for i := range 12 {
		b := Fixture()
		b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/home"`, `"/welcome"`)
		r, err := Prepare(context.Background(), b)
		if err != nil || r.Digest == active.Digest {
			t.Fatal("SITE_TEST_REVISION_PREPARE")
		}
		b["pages.el"] = "invalid"
		delete(b, "init.el")
		for _, rev := range []*Revision{active, r} {
			if _, err := rev.Page(context.Background(), "signed-home"); err != nil {
				t.Fatal("SITE_TEST_CAPTURE_CHANGED")
			}
		}
		if route, _ := active.Named("signed-home"); route.Path != "/home" {
			t.Fatal("SITE_TEST_ACTIVE_CONTAMINATED")
		}
		if i == 0 {
			fresh, err := Prepare(context.Background(), Fixture())
			if err != nil || fresh.Digest != active.Digest {
				t.Fatal("SITE_TEST_REPREPARE_IDENTITY")
			}
		}
	}
}
