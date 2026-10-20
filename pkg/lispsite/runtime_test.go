package lispsite

import (
	"context"
	"embed"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/parser"
)

// These fixtures are self-contained library examples, not application assets.
//
//go:embed testdata/*.el
var fixtureFiles embed.FS

func fixture() Bundle {
	files := Bundle{}
	entries, _ := fixtureFiles.ReadDir("testdata")
	for _, entry := range entries {
		b, _ := fixtureFiles.ReadFile("testdata/" + entry.Name())
		files[entry.Name()] = string(b)
	}
	return files
}
func fixturePolicy() Policy {
	return Policy{ReservedPaths: []string{"/private"}, AllowedActions: []Action{{"demo/send", "login-form"}, {"demo/clear", "post-form"}}}
}
func prepare(ctx context.Context, b Bundle) (*Revision, error) {
	return Prepare(ctx, b, fixturePolicy())
}
func prepared(t *testing.T) *Revision {
	t.Helper()
	r, err := prepare(context.Background(), fixture())
	if err != nil {
		t.Fatal("LISPSITE_TEST_PREPARE")
	}
	return r
}
func diagnostic(t *testing.T, err error) *Diagnostic {
	t.Helper()
	d, ok := err.(*Diagnostic)
	if !ok || d == nil {
		t.Fatal("LISPSITE_TEST_DIAGNOSTIC")
	}
	return d
}
func nodeText(n Node, kind string) string {
	if n.Kind == kind {
		return n.Text
	}
	for _, child := range n.Children {
		if text := nodeText(child, kind); text != "" {
			return text
		}
	}
	return ""
}

func TestFixtureStages(t *testing.T) {
	a, _ := copyPolicy(fixturePolicy())
	r := &Revision{Groups: map[string]string{}, admission: a}
	p := &preparation{fixture(), map[string]bool{}, map[string]bool{}, r}
	ctx := context.WithValue(context.Background(), phaseKey{}, &execution{preparing: p, revision: r})
	e, policy, err := environment(ctx)
	if err != nil {
		t.Fatal("LISPSITE_TEST_BOOTSTRAP")
	}
	for _, name := range []string{"shared.el", "options.el", "pages.el", "routes.el", "style.el"} {
		if v := p.load(e, name); v.Type == lisp.LError {
			t.Fatal("LISPSITE_TEST_STAGE")
		}
	}
	if !validRevision(e, r) || len(r.Options) != 1 || r.Options[0].Default != true {
		t.Fatal("LISPSITE_TEST_METADATA")
	}
	plan, err := lisp.NewTemplate(e, policy)
	if err != nil {
		t.Fatal("LISPSITE_TEST_TEMPLATE")
	}
	r.template = plan
	e.Put(lisp.Symbol("front"), lisp.String("synthetic-source-mutation"))
	for _, route := range r.Routes {
		if route.Method == "GET" {
			if _, err := r.Page(ctx, route.Entry); err != nil {
				t.Fatal("LISPSITE_TEST_PAGE")
			}
		}
	}
}

func TestBundleRefusals(t *testing.T) {
	for _, change := range []func(Bundle){
		func(b Bundle) { b["init.el"] = `(site:load "init.el")` },
		func(b Bundle) { b["init.el"] += `(site:load "shared.el")` },
		func(b Bundle) { b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/details"`, `"/private/child"`) },
		func(b Bundle) { b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/details"`, `"/%64etails"`) },
		func(b Bundle) {
			b["routes.el"] = strings.ReplaceAll(b["routes.el"], `'details "/details"`, `'form "/details"`)
		},
		func(b Bundle) { b["routes.el"] += `(site:get 'duplicate "/" 'public 'front)` },
		func(b Bundle) { b["routes.el"] = strings.ReplaceAll(b["routes.el"], "'demo/send", "'demo/unknown") },
		func(b Bundle) {
			b["routes.el"] = strings.ReplaceAll(b["routes.el"], "'details 'form", "'send 'missing")
		},
		func(b Bundle) {
			b["routes.el"] = strings.ReplaceAll(b["routes.el"], "'public 'front", "'public 'missing")
		},
		func(b Bundle) {
			b["style.el"] = `(site:style '((paper "url(private)") (ink "#111111") (link "#111111") (border "#111111")))`
		},
		func(b Bundle) {
			b["options.el"] += `(defcustom show-introduction false "duplicate" :type 'boolean :group 'display)`
		},
		func(b Bundle) {
			b["options.el"] = strings.ReplaceAll(b["options.el"], "show-introduction true", `show-introduction "true"`)
		},
		func(b Bundle) {
			b["pages.el"] += `(defun front (v p) (ui:document "bad" (ui:main (ui:link "https://invalid.example" "bad"))))`
		},
		func(b Bundle) {
			b["pages.el"] += `(defun front (v p) (ui:document "bad" (ui:main (ui:login-form 'clear))))`
		},
		func(b Bundle) {
			b["pages.el"] += `(defun front (v p) (ui:document "bad" (ui:main (ui:post-form 'send "bad"))))`
		},
		func(b Bundle) { b["pages.el"] += `(debug-print "not available")` },
	} {
		b := fixture()
		change(b)
		if _, err := prepare(context.Background(), b); err == nil {
			t.Fatal("LISPSITE_TEST_UNSAFE_ADMISSION")
		}
	}
	prepared(t)
}

func TestCapturedSourcesAndPolicy(t *testing.T) {
	active := prepared(t)
	for range 12 {
		b, policy := fixture(), fixturePolicy()
		b["routes.el"] = strings.ReplaceAll(b["routes.el"], `"/details"`, `"/example"`)
		r, err := Prepare(context.Background(), b, policy)
		if err != nil || r.Digest == active.Digest {
			t.Fatal("LISPSITE_TEST_REVISION")
		}
		b["pages.el"] = "invalid"
		delete(b, "init.el")
		policy.ReservedPaths[0] = "/example"
		policy.AllowedActions[0] = Action{"demo/send", "post-form"}
		for _, rev := range []*Revision{active, r} {
			if _, err := rev.Page(context.Background(), "form-page"); err != nil || rev.admission.reserved("/example") {
				t.Fatal("LISPSITE_TEST_CAPTURE")
			}
		}
		if route, _ := active.Named("details"); route.Path != "/details" {
			t.Fatal("LISPSITE_TEST_CONTAMINATION")
		}
	}
	fresh := prepared(t)
	if fresh.Digest != active.Digest {
		t.Fatal("LISPSITE_TEST_DIGEST")
	}
}

func TestProductionCapabilityAndAdmissionPolicy(t *testing.T) {
	e, policy, err := environment(context.Background())
	if err != nil {
		t.Fatal("LISPSITE_TEST_ENVIRONMENT")
	}
	for _, name := range []string{"load-file", "load-string", "eval", "funcall", "apply", "debug-print", "debug-stack", "in-package", "use-package", "export", "get-function", "testing:test", "time:sleep", "json:load", "http:get"} {
		if e.Get(lisp.Symbol(name)).Type == lisp.LFun {
			t.Fatal("LISPSITE_TEST_CAPABILITY")
		}
	}
	if e.Runtime.Library != nil || e.Runtime.LoadCache != nil {
		t.Fatal("LISPSITE_TEST_SOURCE_SERVICE")
	}
	e.InPackage(lisp.Symbol("lisp"))
	e.AddBuiltins(true, builtin{"unreviewed", lisp.Formals(), func(e *lisp.LEnv, _ *lisp.LVal) *lisp.LVal { return lisp.Nil() }})
	if _, err := lisp.NewTemplate(e, policy); err == nil {
		t.Fatal("LISPSITE_TEST_UNREVIEWED_CALLBACK")
	}
	e, policy, _ = environment(context.Background())
	e.Put(lisp.Symbol("opaque"), lisp.Native(map[string]int{"n": 0}))
	if _, err := lisp.NewTemplate(e, policy); err == nil {
		t.Fatal("LISPSITE_TEST_NATIVE")
	}
	r := prepared(t)
	vm, err := r.template.NewVM()
	if err != nil || vm.Runtime.Reader != nil || vm.Runtime.Library != nil || vm.Runtime.LoadCache != nil {
		t.Fatal("LISPSITE_TEST_REQUEST_SOURCE")
	}
	for _, source := range []string{`(site:load "pages.el")`, `(site:get 'x "/x" 'public 'front)`, `(site:post 'x "/x" 'public 'demo/send 'front 'front)`, `(site:group 'x "x")`, `(site:custom 'x true "x" 'boolean 'display)`, `(site:style '())`} {
		ctx := context.WithValue(context.Background(), phaseKey{}, &execution{revision: r})
		exprs, err := parser.NewReader().Read("synthetic", strings.NewReader(source))
		if err != nil || len(exprs) != 1 || vm.EvalContext(ctx, exprs[0]).Type != lisp.LError {
			t.Fatal("LISPSITE_TEST_REQUEST_DECLARATION")
		}
	}
}

func TestRuntimeNegativeBounds(t *testing.T) {
	for _, source := range []string{
		`(dotimes (i 2147483647))`, `(defun spin () (spin)) (spin)`, `(defun deep () (+ 1 (deep))) (deep)`,
		`(defmacro forever () (forever)) (forever)`,
		`(set 's "xxxxxxxxxxxxxxxx") (dotimes (i 20) (set! s (concat 'string s s)))`,
		strings.Repeat("(", 2048) + "0" + strings.Repeat(")", 2048), `(site:load "missing.el")`,
	} {
		b := fixture()
		b["init.el"] = source
		start := time.Now()
		if _, err := prepare(context.Background(), b); err == nil || time.Since(start) > 2*time.Second {
			t.Fatal("LISPSITE_TEST_PREPARATION_BOUND")
		}
	}
	for _, change := range []func(Bundle){
		func(b Bundle) { b[strings.Repeat("a", 128)+".el"] = "" },
		func(b Bundle) { b["extra.el"] = "; unused" },
		func(b Bundle) { b["init.el"] = strings.Repeat(" ", maxBundleBytes+1) },
		func(b Bundle) {
			b["pages.el"] += `(defun front (v p) (ui:document "x" (ui:main (ui:group ` + strings.Repeat(`(ui:p "x") `, MaxNodes) + `))))`
		},
		func(b Bundle) { b["routes.el"] += strings.Repeat(`(site:get 'x "/x" 'public 'front)`, maxRoutes) },
	} {
		b := fixture()
		change(b)
		if _, err := prepare(context.Background(), b); err == nil {
			t.Fatal("LISPSITE_TEST_BOUND_ADMISSION")
		}
	}
	b := fixture()
	b["pages.el"] += `(defun spin (v p) (spin v p)) (defun deep (v p) (+ 1 (deep v p)))`
	r, err := prepare(context.Background(), b)
	if err != nil {
		t.Fatal("LISPSITE_TEST_LIMIT_FIXTURE")
	}
	for _, entry := range []string{"spin", "deep"} {
		if _, err := r.Page(context.Background(), entry); err == nil {
			t.Fatal("LISPSITE_TEST_PAGE_BOUND")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepare(ctx, fixture()); err == nil {
		t.Fatal("LISPSITE_TEST_CANCELLED_PREPARE")
	}
	if _, err := r.Page(ctx, "front"); err == nil {
		t.Fatal("LISPSITE_TEST_CANCELLED_PAGE")
	}
	if _, err := r.Page(context.Background(), "front"); err != nil {
		t.Fatal("LISPSITE_TEST_CANCEL_CONTAMINATION")
	}
}

// Exact-identity synthetic callback; no production capability or Go captures.
type cancellationProbeKey struct{}
type cancellationProbe struct{ entered, cleaned chan struct{} }

func cancellationProbeCallback(e *lisp.LEnv, _ *lisp.LVal) *lisp.LVal {
	probe := e.Context().Value(cancellationProbeKey{}).(*cancellationProbe)
	defer close(probe.cleaned)
	close(probe.entered)
	<-e.Context().Done()
	return e.CheckContext()
}
func TestPageCancellationAndHealthyPeer(t *testing.T) {
	healthy := prepared(t)
	e := lisp.NewEnv(nil)
	e.Runtime.Registry.Lang = "user"
	e.DefinePackage(lisp.Symbol("user"))
	e.InPackage(lisp.Symbol("user"))
	e.AddBuiltins(true, builtin{"wait", lisp.Formals("v", "p"), cancellationProbeCallback})
	approved := e.Runtime.Registry.RegisteredBuiltin("user", "wait")
	plan, err := lisp.NewTemplate(e, lisp.TemplateWithBuiltinPolicy(func(f *lisp.LVal) bool { return f == approved }))
	if err != nil {
		t.Fatal("LISPSITE_TEST_CANCEL_TEMPLATE")
	}
	r := &Revision{template: plan}
	probe := &cancellationProbe{make(chan struct{}), make(chan struct{})}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), cancellationProbeKey{}, probe))
	defer cancel()
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		select {
		case <-probe.entered:
			if _, err := healthy.Page(context.Background(), "front"); err != nil {
				t.Error("LISPSITE_TEST_HEALTHY_PEER")
			}
		case <-time.After(time.Second):
			t.Error("LISPSITE_TEST_NOT_ENTERED")
		}
		cancel()
	}()
	_, err = r.Page(ctx, "wait")
	<-peerDone
	if diagnostic(t, err).Category != Cancelled {
		t.Fatal("LISPSITE_TEST_ACTIVE_CANCELLATION")
	}
	select {
	case <-probe.cleaned:
	default:
		t.Fatal("LISPSITE_TEST_CLEANUP_NOT_JOINED")
	}
}

func TestRuntimeDiagnosticClassificationAndPrivacy(t *testing.T) {
	e, _, err := environment(context.Background())
	if err != nil {
		t.Fatal("LISPSITE_TEST_ENVIRONMENT")
	}
	for _, tc := range []struct{ condition, constraint string }{
		{lisp.CondStepLimitExceeded, "step_budget"}, {lisp.CondStepBudgetExceeded, "step_budget"},
		{lisp.CondEvalNestingExceeded, "evaluation_nesting"}, {"site-policy-refused", "declared_presentation_capability"},
		{lisp.CondInternalPanic, "bounded_complete_page_cause_unknown"}, {"synthetic-hostile-condition", "bounded_complete_page_cause_unknown"},
	} {
		d := evaluationFault(context.Background(), e.ErrorConditionf(tc.condition, "synthetic-secret-never-disclose"), false)
		wire, err := json.Marshal(d)
		if err != nil || d.Stage != "page_evaluation" || d.Constraint != tc.constraint || strings.Contains(string(wire)+d.Error(), "synthetic-") {
			t.Fatal("LISPSITE_TEST_DIAGNOSTIC_PRIVACY")
		}
	}
	r := prepared(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	observed := e.ErrorConditionf(lisp.CondStepLimitExceeded, "synthetic-secret-never-disclose")
	if d := evaluationFault(ctx, observed, false); d.Constraint != "step_budget" {
		t.Fatal("LISPSITE_TEST_PRIMARY_ERASED")
	}
	_, err = r.Page(ctx, "front")
	if diagnostic(t, err).Category != Cancelled {
		t.Fatal("LISPSITE_TEST_CANCEL_CATEGORY")
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = prepare(ctx, fixture())
	if diagnostic(t, err).Category != DeadlineExceeded {
		t.Fatal("LISPSITE_TEST_DEADLINE_CATEGORY")
	}
	b := fixture()
	b["init.el"] = `(dotimes (i 2147483647))`
	_, err = prepare(context.Background(), b)
	if d := diagnostic(t, err); d.Code != "EXECUTION_LIMIT" || d.Constraint != "step_budget" {
		t.Fatal("LISPSITE_TEST_STEP_CATEGORY")
	}
}

func TestTemplateGraphDepthRefusal(t *testing.T) {
	e, policy, err := environment(context.Background())
	if err != nil {
		t.Fatal("LISPSITE_TEST_ENVIRONMENT")
	}
	value := lisp.Nil()
	for range 1100 {
		value = lisp.QExpr([]*lisp.LVal{value})
	}
	e.Put(lisp.Symbol("deep-graph"), value)
	if _, err := lisp.NewTemplate(e, policy); err == nil {
		t.Fatal("LISPSITE_TEST_GRAPH_LIMIT")
	}
}

func TestSourceClosureAndPreferencesIsolation(t *testing.T) {
	b := fixture()
	b["options.el"] += `(set 'next (let ((n 0)) (lambda () (set! n (+ n 1)) n)))`
	b["pages.el"] += `(defun details (v p) (page "Details" (ui:p (if (= (next) 1) (if (site:option p 'show-introduction) (progn (assoc! p "show-introduction" false) "clean") "leaked") "leaked"))))`
	r, err := prepare(context.Background(), b)
	if err != nil {
		t.Fatal("LISPSITE_TEST_CLOSURE_PREPARE")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 8 {
				p, err := r.Page(context.Background(), "details")
				if err != nil || nodeText(p, "p") != "clean" {
					t.Error("LISPSITE_TEST_CLOSURE_OR_PREFERENCE_LEAK")
				}
			}
		})
	}
	wg.Wait()
}

func TestCooperatingCallbackCancellationAndCleanup(t *testing.T) {
	r := prepared(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vm, err := r.template.NewVM(lisp.VMWithContext(ctx))
	if err != nil {
		t.Fatal("LISPSITE_TEST_CANCEL_VM")
	}
	entered, cleaned := make(chan struct{}), make(chan struct{})
	vm.AddBuiltins(true, builtin{"wait", lisp.Formals(), func(e *lisp.LEnv, _ *lisp.LVal) *lisp.LVal {
		defer close(cleaned)
		close(entered)
		<-e.Context().Done()
		return e.Errorf("LISPSITE_TEST_CALLBACK_CANCELLED")
	}})
	cancelDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		select {
		case <-entered:
			cancel()
		case <-time.After(time.Second):
			cancel()
		}
	}()
	v := vm.FunCallContext(ctx, vm.Get(lisp.Symbol("wait")), lisp.Nil())
	<-cancelDone
	if v.Type != lisp.LError || ctx.Err() == nil {
		t.Fatal("LISPSITE_TEST_CALLBACK_CANCEL")
	}
	select {
	case <-cleaned:
	default:
		t.Fatal("LISPSITE_TEST_CALLBACK_CLEANUP_NOT_JOINED")
	}
	if _, err := r.Page(context.Background(), "front"); err != nil {
		t.Fatal("LISPSITE_TEST_HEALTH_AFTER_CANCEL")
	}
}

func TestBundleCancellationAndIsolation(t *testing.T) {
	b := fixture()
	b["options.el"] += `(set 'counter (sorted-map "n" 0))`
	b["pages.el"] += `(defun details (v p) (assoc! counter "n" (+ (get counter "n") 1)) (page "Details" (ui:p (if (= (get counter "n") 1) "clean" "leaked"))))`
	r, err := prepare(context.Background(), b)
	if err != nil {
		t.Fatal("LISPSITE_TEST_ISOLATION_PREPARE")
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 8 {
				n, err := r.Page(context.Background(), "details")
				if err != nil || nodeText(n, "p") != "clean" {
					t.Error("LISPSITE_TEST_VM_STATE_LEAK")
				}
			}
		})
	}
	wg.Wait()
	b = fixture()
	b["pages.el"] += `(defun spin () (spin)) (spin)`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := prepare(ctx, b); err == nil || time.Since(start) > time.Second {
		t.Fatal("LISPSITE_TEST_PREPARE_UNBOUNDED")
	}
}
