// Package lispsite prepares bounded trusted ELPS sites and evaluates pages in
// isolated request VMs. It provides presentation data, not HTTP or backend actions.
package lispsite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/luthersystems/elps/lisp"
	"github.com/luthersystems/elps/parser"
)

const maxFiles = 8
const maxBundleBytes = 32 << 10

// MaxNodes bounds the presentation tree shared with host renderers.
const MaxNodes = 256

// MaxTextBytes bounds copied presentation text before host escaping.
const MaxTextBytes = 32 << 10

// maxViewEntries bounds entries in a caller-supplied page view.
const maxViewEntries = 32

const maxRoutes = 5
const revisionBudget = 100000

type Bundle map[string]string

type Route struct{ Name, Method, Path, Policy, Entry, Action, Success, Error string }
type Option struct {
	Name, Group, Help, Type string
	Choices                 []string
	Default                 any
}
type Style struct{ Paper, Ink, Link, Border string }
type Revision struct {
	Digest    string
	Routes    []Route
	Options   []Option
	Groups    map[string]string
	Style     Style
	template  *lisp.Template
	admission admission
}
type phaseKey struct{}
type execution struct {
	preparing *preparation
	revision  *Revision
}
type preparation struct {
	files    Bundle
	loaded   map[string]bool
	loading  map[string]bool
	revision *Revision
}

func bundleFault() *Diagnostic {
	return fault("BUNDLE_REFUSED", "bundle_prepare", "bounded_valid_revision", Refused)
}
func renderFault() *Diagnostic {
	return fault("RENDER_UNAVAILABLE", "page_evaluation", "bounded_complete_page_cause_unknown", RenderUnknown)
}
func viewFault() *Diagnostic {
	return fault("PAGE_VIEW_REFUSED", "page_evaluation", "bounded_string_view", Refused)
}

// Read only SDK condition tags, never formatted errors, source or payloads.
// Host adapters may project these diagnostics without losing known evidence.
func evaluationFault(ctx context.Context, v *lisp.LVal, preparing bool) *Diagnostic {
	stage := "page_evaluation"
	fallback := renderFault()
	if preparing {
		stage = "bundle_prepare"
		fallback = fault("BUNDLE_UNAVAILABLE", stage, "preparation_cause_unknown", SetupUnknown)
	}
	if v != nil && v.Type == lisp.LError {
		switch (*lisp.ErrorVal)(v).Condition() {
		case lisp.CondStepLimitExceeded, lisp.CondStepBudgetExceeded:
			return fault("EXECUTION_LIMIT", stage, "step_budget", Refused)
		case lisp.CondEvalNestingExceeded:
			return fault("EXECUTION_LIMIT", stage, "evaluation_nesting", Refused)
		case "site-policy-refused":
			if preparing {
				return bundleFault()
			}
			return fault("PAGE_REFUSED", stage, "declared_presentation_capability", Refused)
		case lisp.CondInternalPanic:
			return fallback
		}
	}
	if ctx.Err() != nil {
		kind, constraint := Cancelled, "context_cancelled"
		if ctx.Err() == context.DeadlineExceeded {
			kind, constraint = DeadlineExceeded, "context_deadline"
		}
		return fault("EXECUTION_INTERRUPTED", stage, constraint, kind)
	}
	if preparing {
		return bundleFault()
	}
	return fallback
}

var sourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}\.el$`)
var stableName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var routePath = regexp.MustCompile(`^/(?:[a-z0-9-]+(?:/[a-z0-9-]+)*)?$`)
var color = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Prepare owns the captured bounded bytes. No disk/network library, source cache,
// background worker or backend capability participates in initialization.
func Prepare(ctx context.Context, files Bundle, policy Policy) (revision *Revision, result error) {
	defer func() {
		if recover() != nil {
			revision = nil
			result = fault("BUNDLE_UNAVAILABLE", "bundle_prepare", "preparation_cause_unknown", SetupUnknown)
		}
	}()
	if ctx.Err() != nil {
		return nil, evaluationFault(ctx, nil, true)
	}
	admission, ok := copyPolicy(policy)
	if !ok {
		return nil, bundleFault()
	}
	if len(files) == 0 || len(files) > maxFiles {
		return nil, bundleFault()
	}
	copied := make(Bundle, len(files))
	names := make([]string, 0, len(files))
	size := 0
	for name, text := range files {
		if !sourceName.MatchString(name) || len(text) > maxBundleBytes-size {
			return nil, bundleFault()
		}
		size += len(text)
		copied[name] = text
		names = append(names, name)
	}
	sort.Strings(names)
	digest := sha256.New()
	for _, name := range names {
		digest.Write([]byte(name))
		digest.Write([]byte{0})
		digest.Write([]byte(copied[name]))
		digest.Write([]byte{0})
	}
	r := &Revision{Digest: hex.EncodeToString(digest.Sum(nil)), Groups: map[string]string{}, Style: Style{"#fafafa", "#383a42", "#3a5fcd", "#6b6d75"}, admission: admission}
	p := &preparation{copied, map[string]bool{}, map[string]bool{}, r}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, phaseKey{}, &execution{preparing: p, revision: r})
	e, builtinPolicy, err := environment(ctx)
	if err != nil {
		return nil, err
	}
	if v := p.load(e, "init.el"); v.Type == lisp.LError {
		return nil, evaluationFault(ctx, v, true)
	}
	if ctx.Err() != nil {
		return nil, evaluationFault(ctx, nil, true)
	}
	if len(p.loaded) != len(copied) || !validRevision(e, r) {
		return nil, bundleFault()
	}
	// Parsing is preparation-only. Do not share a reader, source library or
	// cache with request VMs; no request callback can reopen captured source.
	e.Runtime.Reader = nil
	plan, err := lisp.NewTemplate(e, builtinPolicy)
	if ctx.Err() != nil {
		return nil, evaluationFault(ctx, nil, true)
	}
	if err != nil {
		return nil, bundleFault()
	}
	r.template = plan
	// Admission evaluates every declared page in a disposable request VM.
	for _, route := range r.Routes {
		if route.Method == "GET" {
			if _, err := r.Page(ctx, route.Entry); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

func (p *preparation) load(e *lisp.LEnv, name string) *lisp.LVal {
	if v := e.CheckContext(); v.Type == lisp.LError {
		return v
	}
	text, found := p.files[name]
	if !found || p.loaded[name] || p.loading[name] {
		return e.ErrorConditionf("site-policy-refused", "SITE_LOAD_REFUSED")
	}
	p.loading[name] = true
	// Host callbacks execute in their registered package. Bundle definitions
	// always belong to user, including files loaded from site:load.
	previous := e.Runtime.Package.Name
	e.InPackage(lisp.Symbol("user"))
	defer e.InPackage(lisp.Symbol(previous))
	v := e.LoadStringContext(e.Context(), name, text)
	delete(p.loading, name)
	if v.Type != lisp.LError {
		p.loaded[name] = true
	}
	return v
}

// Explicit core registrations are stateless upstream functions on env/args.
// No runtime loading, evaluation, registration lookup, output or OS primitives.
func environment(ctx context.Context) (*lisp.LEnv, lisp.TemplateOption, error) {
	e := lisp.NewEnv(nil)
	e.Runtime.Registry.Lang = "lisp"
	e.DefinePackage(lisp.Symbol("lisp"))
	e.InPackage(lisp.Symbol("lisp"))
	allowed := map[*lisp.LVal]bool{}
	install := func(defs []lisp.LBuiltinDef, names []string, add func(bool, ...lisp.LBuiltinDef)) bool {
		for _, name := range names {
			found := false
			for _, d := range defs {
				if d.Name() == name {
					add(true, d)
					allowed[e.Runtime.Registry.RegisteredBuiltin("lisp", name)] = true
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if !install(lisp.DefaultBuiltins(), []string{"set", "list", "cons", "first", "rest", "concat", "get", "sorted-map", "assoc!", "+", "="}, e.AddBuiltins) ||
		!install(lisp.DefaultSpecialOps(), []string{"progn", "if", "lambda", "let", "set!", "dotimes", "quote", "quasiquote"}, e.AddSpecialOps) ||
		!install(lisp.DefaultMacros(), []string{"defun", "defmacro"}, e.AddMacros) {
		return nil, nil, bundleFault()
	}
	for pkg, defs := range map[string][]builtin{
		"site": {{"load", lisp.Formals("name"), loadFile}, {"get", lisp.Formals("name", "path", "policy", "entry"), getRoute}, {"post", lisp.Formals("name", "path", "policy", "action", "success", "error"), postRoute}, {"group", lisp.Formals("name", "help"), optionGroup}, {"custom", lisp.Formals("name", "default", "help", "type", "group"), customOption}, {"style", lisp.Formals("values"), styleValues}, {"url", lisp.Formals("name"), routeURL}, {"option", lisp.Formals("preferences", "name"), optionValue}},
		"ui":   {{"document", lisp.Formals("title", "content"), documentNode}, {"main", lisp.Formals(lisp.VarArgSymbol, "children"), mainNode}, {"group", lisp.Formals(lisp.VarArgSymbol, "children"), groupNode}, {"h1", lisp.Formals("text"), headingNode}, {"p", lisp.Formals("text"), paragraphNode}, {"text", lisp.Formals("text"), textNode}, {"face", lisp.Formals("name", "text"), faceNode}, {"link", lisp.Formals("url", "text"), linkNode}, {"notice", lisp.Formals("suffix"), noticeNode}, {"login-form", lisp.Formals("action"), loginNode}, {"post-form", lisp.Formals("action", "text"), postNode}},
	} {
		e.DefinePackage(lisp.Symbol(pkg))
		e.InPackage(lisp.Symbol(pkg))
		for _, d := range defs {
			e.AddBuiltins(true, d)
			allowed[e.Runtime.Registry.RegisteredBuiltin(pkg, d.name)] = true
		}
	}
	e.DefinePackage(lisp.Symbol("user"))
	e.InPackage(lisp.Symbol("user"))
	e.UsePackage(lisp.Symbol("lisp"))
	for _, c := range []lisp.Config{lisp.WithReader(parser.NewReader()), lisp.WithStderr(io.Discard), lisp.WithContext(ctx), lisp.WithMaxSteps(revisionBudget), lisp.WithMaxAlloc(MaxTextBytes), lisp.WithMaxEvalNesting(128), lisp.WithMaximumPhysicalStackHeight(128), lisp.WithMaxMacroExpansionDepth(64), lisp.WithMaxValueDepth(1024)} {
		if v := c(e); v.Type == lisp.LError {
			return nil, nil, bundleFault()
		}
	}
	e.Runtime.SetStepBudget(revisionBudget)
	return e, lisp.TemplateWithBuiltinPolicy(func(f *lisp.LVal) bool { return allowed[f] }), nil
}

type builtin struct {
	name    string
	formals *lisp.LVal
	fn      lisp.LBuiltin
}

func (b builtin) Name() string                               { return b.name }
func (b builtin) Formals() *lisp.LVal                        { return b.formals }
func (b builtin) Eval(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal { return b.fn(e, a) }
func state(e *lisp.LEnv) *execution                          { s, _ := e.Context().Value(phaseKey{}).(*execution); return s }
func prepareState(e *lisp.LEnv) *preparation {
	s := state(e)
	if s == nil {
		return nil
	}
	return s.preparing
}
func refused(e *lisp.LEnv) *lisp.LVal {
	return e.ErrorConditionf("site-policy-refused", "SITE_DECLARATION_REFUSED")
}
func str(v *lisp.LVal) string {
	if v.Type == lisp.LString || v.Type == lisp.LSymbol {
		return v.Str
	}
	return ""
}
func loadFile(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil || a.Cells[0].Type != lisp.LString {
		return refused(e)
	}
	return p.load(e, a.Cells[0].Str)
}
func getRoute(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil || len(p.revision.Routes) >= maxRoutes {
		return refused(e)
	}
	p.revision.Routes = append(p.revision.Routes, Route{Name: str(a.Cells[0]), Method: "GET", Path: str(a.Cells[1]), Policy: str(a.Cells[2]), Entry: str(a.Cells[3])})
	return lisp.Nil()
}
func postRoute(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil || len(p.revision.Routes) >= maxRoutes {
		return refused(e)
	}
	p.revision.Routes = append(p.revision.Routes, Route{Name: str(a.Cells[0]), Method: "POST", Path: str(a.Cells[1]), Policy: str(a.Cells[2]), Action: str(a.Cells[3]), Success: str(a.Cells[4]), Error: str(a.Cells[5])})
	return lisp.Nil()
}
func optionGroup(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil || len(p.revision.Groups) >= 16 {
		return refused(e)
	}
	name, help := str(a.Cells[0]), str(a.Cells[1])
	if !stableName.MatchString(name) || help == "" || len(help) > 1024 || p.revision.Groups[name] != "" {
		return refused(e)
	}
	p.revision.Groups[name] = help
	return lisp.Nil()
}
func customOption(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil || len(p.revision.Options) >= 16 {
		return refused(e)
	}
	o := Option{Name: str(a.Cells[0]), Help: str(a.Cells[2]), Group: str(a.Cells[4])}
	typ := a.Cells[3]
	if typ.Type == lisp.LSymbol && typ.Str == "boolean" {
		o.Type = "boolean"
		if a.Cells[1].Type != lisp.LSymbol || (a.Cells[1].Str != "true" && a.Cells[1].Str != "false") {
			return refused(e)
		}
		o.Default = a.Cells[1].Str == "true"
	} else {
		if typ.Type != lisp.LSExpr || len(typ.Cells) < 2 || len(typ.Cells) > 9 {
			return refused(e)
		}
		o.Type = str(typ.Cells[0])
		if o.Type != "choice" && o.Type != "ordered-subset" {
			return refused(e)
		}
		seen := map[string]bool{}
		for _, v := range typ.Cells[1:] {
			s := str(v)
			if !stableName.MatchString(s) || seen[s] {
				return refused(e)
			}
			seen[s] = true
			o.Choices = append(o.Choices, s)
		}
		if o.Type == "choice" {
			o.Default = str(a.Cells[1])
		} else {
			v := a.Cells[1]
			if v.Type != lisp.LSExpr {
				return refused(e)
			}
			items := []string{}
			for _, c := range v.Cells {
				items = append(items, str(c))
			}
			o.Default = items
		}
	}
	if !stableName.MatchString(o.Name) || o.Help == "" || len(o.Help) > 1024 || p.revision.Groups[o.Group] == "" || !validOption(o, o.Default) {
		return refused(e)
	}
	for _, other := range p.revision.Options {
		if other.Name == o.Name {
			return refused(e)
		}
	}
	p.revision.Options = append(p.revision.Options, o)
	return lisp.Nil()
}
func validOption(o Option, v any) bool {
	switch o.Type {
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "choice":
		s, ok := v.(string)
		if !ok {
			return false
		}
		for _, c := range o.Choices {
			if s == c {
				return true
			}
		}
	case "ordered-subset":
		xs, ok := v.([]string)
		if !ok || len(xs) > len(o.Choices) {
			return false
		}
		seen := map[string]bool{}
		for _, x := range xs {
			found := false
			for _, c := range o.Choices {
				if c == x {
					found = true
				}
			}
			if !found || seen[x] {
				return false
			}
			seen[x] = true
		}
		return true
	}
	return false
}
func styleValues(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	p := prepareState(e)
	if p == nil {
		return refused(e)
	}
	v := a.Cells[0]
	if v.Type != lisp.LSExpr || len(v.Cells) != 4 {
		return refused(e)
	}
	values := map[string]string{}
	for _, pair := range v.Cells {
		if pair.Type != lisp.LSExpr || len(pair.Cells) != 2 {
			return refused(e)
		}
		k, s := str(pair.Cells[0]), str(pair.Cells[1])
		if values[k] != "" {
			return refused(e)
		}
		values[k] = s
	}
	s := Style{values["paper"], values["ink"], values["link"], values["border"]}
	if !color.MatchString(s.Paper) || !color.MatchString(s.Ink) || !color.MatchString(s.Link) || !color.MatchString(s.Border) {
		return refused(e)
	}
	p.revision.Style = s
	return lisp.Nil()
}
func validRevision(e *lisp.LEnv, r *Revision) bool {
	if len(r.Routes) == 0 || len(r.Routes) > maxRoutes || len(r.Options) > 16 {
		return false
	}
	names := map[string]bool{}
	pairs := map[string]bool{}
	gets := 0
	for _, route := range r.Routes {
		if !stableName.MatchString(route.Name) || !routePath.MatchString(route.Path) || r.admission.reserved(route.Path) || names[route.Name] || pairs[route.Method+route.Path] || (route.Policy != "public" && route.Policy != "session") {
			return false
		}
		names[route.Name] = true
		pairs[route.Method+route.Path] = true
		if route.Method == "GET" {
			gets++
			if !stableName.MatchString(route.Entry) || e.Get(lisp.Symbol(route.Entry)).Type != lisp.LFun {
				return false
			}
		} else if route.Method != "POST" || r.admission.actions[route.Action] == "" {
			return false
		}
	}
	if gets == 0 {
		return false
	}
	for _, route := range r.Routes {
		if route.Method == "POST" {
			for _, dest := range []string{route.Success, route.Error} {
				d, ok := r.Named(dest)
				if !ok || d.Method != "GET" {
					return false
				}
			}
		}
	}
	return true
}
func (r *Revision) Named(name string) (Route, bool) {
	for _, route := range r.Routes {
		if route.Name == name {
			return route, true
		}
	}
	return Route{}, false
}
func routeURL(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	s := state(e)
	if s == nil {
		return refused(e)
	}
	r, ok := s.revision.Named(str(a.Cells[0]))
	if !ok {
		return refused(e)
	}
	return lisp.String(r.Path)
}
func optionValue(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	if a.Cells[0].Type != lisp.LSortMap {
		return refused(e)
	}
	v, ok := a.Cells[0].Map().Get(lisp.String(str(a.Cells[1])))
	if !ok {
		return refused(e)
	}
	return v
}
func node(kind string, values ...*lisp.LVal) *lisp.LVal {
	return lisp.QExpr(append([]*lisp.LVal{lisp.String(kind)}, values...))
}
func documentNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal  { return node("document", a.Cells...) }
func mainNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal      { return node("main", a.Cells...) }
func groupNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal     { return node("group", a.Cells...) }
func headingNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal   { return node("h1", a.Cells...) }
func paragraphNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal { return node("p", a.Cells...) }
func textNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal      { return node("text", a.Cells...) }
func linkNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal      { return node("link", a.Cells...) }
func noticeNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal    { return node("notice", a.Cells...) }
func loginNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal     { return node("login-form", a.Cells...) }
func postNode(_ *lisp.LEnv, a *lisp.LVal) *lisp.LVal      { return node("post-form", a.Cells...) }

// Faces are closed presentation choices, never caller-supplied CSS/classes.
func faceNode(e *lisp.LEnv, a *lisp.LVal) *lisp.LVal {
	if a.Cells[0].Type != lisp.LSymbol || a.Cells[0].Str != "tooltip" {
		return refused(e)
	}
	return node("tooltip", a.Cells[1])
}

type Node struct {
	Kind, Text, Target string
	Children           []Node
}

// Page evaluates one page entry with the legacy empty view.
func (r *Revision) Page(ctx context.Context, entry string) (Node, error) {
	return r.PageWithView(ctx, entry, nil)
}

// PageWithView evaluates one page entry with a bounded copy of caller-supplied
// view strings as the page's first argument. Nil and empty views are valid. The
// caller must not mutate view while this call runs; nothing retains it after.
func (r *Revision) PageWithView(ctx context.Context, entry string, view map[string]string) (page Node, result error) {
	defer func() {
		if recover() != nil {
			page = Node{}
			result = renderFault()
		}
	}()
	if ctx.Err() != nil {
		return Node{}, evaluationFault(ctx, nil, false)
	}
	if r == nil || r.template == nil || !stableName.MatchString(entry) {
		return Node{}, renderFault()
	}
	viewMap, ok := copyView(view)
	if !ok {
		return Node{}, viewFault()
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, phaseKey{}, &execution{revision: r})
	e, err := r.template.NewVM(lisp.VMWithContext(ctx), lisp.VMWithStderr(io.Discard), lisp.VMWithStepBudget(20000))
	if ctx.Err() != nil {
		return Node{}, evaluationFault(ctx, nil, false)
	}
	if err != nil {
		return Node{}, renderFault()
	}
	prefs := lisp.SortedMap()
	for _, o := range r.Options {
		v := lisp.Nil()
		switch x := o.Default.(type) {
		case bool:
			v = lisp.Bool(x)
		case string:
			v = lisp.String(x)
		case []string:
			cells := []*lisp.LVal{}
			for _, s := range x {
				cells = append(cells, lisp.Symbol(s))
			}
			v = lisp.QExpr(cells)
		}
		if result := prefs.Map().Set(lisp.String(o.Name), v); result.Type == lisp.LError {
			return Node{}, renderFault()
		}
	}
	f := e.Get(lisp.Symbol(entry))
	if f.Type != lisp.LFun {
		return Node{}, renderFault()
	}
	v := e.FunCallContext(ctx, f, lisp.SExpr([]*lisp.LVal{viewMap, prefs}))
	if v.Type == lisp.LError {
		return Node{}, evaluationFault(ctx, v, false)
	}
	count, text := 0, 0
	page, ok = r.copyNode(v, 0, &count, &text)
	if ctx.Err() != nil {
		return Node{}, evaluationFault(ctx, nil, false)
	}
	if !ok || page.Kind != "document" {
		return Node{}, renderFault()
	}
	return page, nil
}

// copyView builds an independent bounded sorted-map. Only string keys and
// values enter it, so the page function receives no callback or SDK value.
func copyView(view map[string]string) (*lisp.LVal, bool) {
	if len(view) > maxViewEntries {
		return nil, false
	}
	m := lisp.SortedMapSized(len(view))
	total := 0
	for k, v := range view {
		// Bound size before scanning UTF-8. stableName bounds key length; a
		// negative remaining budget rejects even an empty value without overflow.
		if !stableName.MatchString(k) || len(v) > MaxTextBytes-total-len(k) || !utf8.ValidString(v) {
			return nil, false
		}
		total += len(k) + len(v)
		if result := m.Map().Set(lisp.String(k), lisp.String(v)); result.Type == lisp.LError {
			return nil, false
		}
	}
	return m, true
}

func (r *Revision) copyNode(v *lisp.LVal, depth int, count, text *int) (Node, bool) {
	*count++
	if depth > 16 || *count > MaxNodes || v.Type != lisp.LSExpr || len(v.Cells) == 0 || v.Cells[0].Type != lisp.LString {
		return Node{}, false
	}
	n := Node{Kind: v.Cells[0].Str}
	args := v.Cells[1:]
	take := func(v *lisp.LVal) (string, bool) {
		if v.Type != lisp.LString {
			return "", false
		}
		*text += len(v.Str)
		return v.Str, *text <= MaxTextBytes
	}
	switch n.Kind {
	case "document":
		if depth != 0 || len(args) != 2 {
			return Node{}, false
		}
		var ok bool
		n.Text, ok = take(args[0])
		if !ok {
			return Node{}, false
		}
		child, ok := r.copyNode(args[1], depth+1, count, text)
		if !ok || child.Kind != "main" {
			return Node{}, false
		}
		n.Children = []Node{child}
	case "main", "group":
		for _, v := range args {
			child, ok := r.copyNode(v, depth+1, count, text)
			if !ok || child.Kind == "document" || child.Kind == "main" {
				return Node{}, false
			}
			n.Children = append(n.Children, child)
		}
	case "h1", "p", "text", "notice", "tooltip":
		if len(args) != 1 {
			return Node{}, false
		}
		var ok bool
		n.Text, ok = take(args[0])
		if !ok {
			return Node{}, false
		}
	case "link":
		if len(args) != 2 {
			return Node{}, false
		}
		var ok bool
		n.Target, ok = take(args[0])
		if !ok {
			return Node{}, false
		}
		n.Text, ok = take(args[1])
		if !ok {
			return Node{}, false
		}
		found := false
		for _, route := range r.Routes {
			if route.Method == "GET" && route.Path == n.Target {
				found = true
			}
		}
		if !found {
			return Node{}, false
		}
	case "login-form", "post-form":
		if (n.Kind == "login-form" && len(args) != 1) || (n.Kind == "post-form" && len(args) != 2) {
			return Node{}, false
		}
		name := str(args[0])
		route, ok := r.Named(name)
		if !ok || route.Method != "POST" || r.admission.actions[route.Action] != n.Kind {
			return Node{}, false
		}
		n.Target = route.Path
		if n.Kind == "post-form" {
			if len(args) != 2 {
				return Node{}, false
			}
			n.Text, ok = take(args[1])
			if !ok {
				return Node{}, false
			}
		}
	default:
		return Node{}, false
	}
	return n, true
}
