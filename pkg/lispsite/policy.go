package lispsite

import (
	"regexp"
	"strings"
)

// Policy is admission data, not executable capabilities. Prepare copies all
// slices; subsequent caller mutations cannot change an admitted revision.
// ReservedPaths reserves each path and its descendants. ReservedPrefixes uses
// literal prefix matching (including partial path segments).
type Policy struct {
	ReservedPaths    []string
	ReservedPrefixes []string
	AllowedActions   []Action
	HostComponents   []string // inert leaf names; interpretation belongs only to the host
}

// Action associates a host-owned action name with a closed presentation kind:
// "login-form" (credential entry) or "post-form" (a submit button). The library
// never implements or invokes actions, authentication or authorization.
type Action struct{ Name, Form string }

type admission struct {
	paths, prefixes []string
	actions         map[string]string
	hosts           map[string]bool
}

var policyPath = regexp.MustCompile(`^/(?:[a-z0-9_-]+(?:/[a-z0-9_-]+)*)?$`)
var actionName = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:/[a-z][a-z0-9-]*)*$`)

func copyPolicy(p Policy) (admission, bool) {
	if len(p.ReservedPaths) > 32 || len(p.ReservedPrefixes) > 32 || len(p.AllowedActions) > 32 || len(p.HostComponents) > 32 {
		return admission{}, false
	}
	a := admission{paths: append([]string(nil), p.ReservedPaths...), prefixes: append([]string(nil), p.ReservedPrefixes...), actions: map[string]string{}, hosts: map[string]bool{}}
	for _, paths := range [][]string{a.paths, a.prefixes} {
		for _, path := range paths {
			if len(path) > 256 || !policyPath.MatchString(path) {
				return admission{}, false
			}
		}
	}
	for _, action := range p.AllowedActions {
		if len(action.Name) > 128 || !actionName.MatchString(action.Name) || a.actions[action.Name] != "" || (action.Form != "login-form" && action.Form != "post-form") {
			return admission{}, false
		}
		a.actions[action.Name] = action.Form
	}
	for _, name := range p.HostComponents {
		if !stableName.MatchString(name) || a.hosts[name] {
			return admission{}, false
		}
		a.hosts[name] = true
	}
	return a, true
}

func (a admission) reserved(path string) bool {
	for _, p := range a.paths {
		if p == "/" || path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	for _, p := range a.prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// Ready reports whether this revision owns an admitted VM template. Public
// metadata is presentation data; callers must not mutate it while serving.
func (r *Revision) Ready() bool { return r != nil && r.template != nil }
