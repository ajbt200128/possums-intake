# lispsite

A reusable Go package for bounded, trusted **ELPS** website presentation. ELPS is
not Emacs Lisp; the `.el` naming and small UI declarations are application
conventions. This package lives in the existing module, not a separate published
module or repository.

## Boundary

- Direct dependencies: Go standard library and the pinned ELPS library only.
- Owns captured source loading, declaration admission, template preparation,
  fresh request VMs and copying bounded presentation nodes into Go values.
- Does **not** implement HTTP, HTML/CSS rendering, authentication, sessions,
  PocketBase storage, accounting, publication, logging or telemetry.
- The host supplies reserved paths/prefixes and allowed action/form bindings as
  copied `Policy` data. The library never executes backend actions.
- `Diagnostic` exposes closed content-free evidence. Application adapters must
  validate complete tuples before mapping them; never forward upstream errors.

## Small consumer

```go
revision, err := lispsite.Prepare(ctx, lispsite.Bundle{
    "init.el": `(defun welcome (view preferences)
                   (ui:document "Example" (ui:main (ui:p "Hello"))))
                (site:get 'welcome "/" 'public 'welcome)`,
}, lispsite.Policy{})
if err != nil {
    return err
}
page, err := revision.Page(ctx, "welcome")
// The host validates/renders page with escaping; it is not raw HTML.
```

`revision.PageWithView(ctx, entry, view)` is the view-aware form. `Page` is the
legacy form and delegates to it with a nil view, so its behavior is unchanged.

Import using `possums-intake/pkg/lispsite`. The import path reflects the current
module name, not an intake dependency. `consumer_test.go` exercises an external
single-page consumer and checks direct/transitive dependency boundaries.

## Serving contract

Preparation captures source bytes once. Each `Page` creates an independent VM
with request cancellation, step/nesting limits and bounded copied output. No
request parser/source-loading service, shared mutable VM or background worker.
SDK construction/parser/native calls cannot be forcibly preempted; these bounds
are not a whole-process memory or capacity guarantee.

Treat returned revision metadata as read-only while serving; it is not deeply
immutable Go data. Do not mutate routes/options/maps concurrently with requests.
Keep request-specific data and services out of shared callbacks/templates.

`internal/website` is the intake adapter: it retains application route admission,
renderer/style/assets, SCS/nosurf/SDK integration and existing failure projection.

## Page view

The page entry receives `view` as its first argument and `preferences` as its
second. A view is a caller-supplied `map[string]string` of presentation strings,
not a service, callback or structured object graph. `PageWithView` requires:

- At most 32 entries, keys matching the same stable lowercase name rule as route
  names, and UTF-8-valid values.
- At most `MaxTextBytes` of total key plus value bytes.

Nil and empty views are valid. The caller must not mutate the map while the call
runs; the library copies it into a fresh sorted-map for one request VM and retains
neither the map nor its strings afterwards. Invalid views return the closed
`PAGE_VIEW_REFUSED` / `page_evaluation` / `bounded_string_view` / `refused`
diagnostic, which never echoes the offending keys or values. A page function may
mutate its own copy without affecting the caller's map.
