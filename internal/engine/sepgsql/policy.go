// Package sepgsql owns immutable, explicitly simulated SELinux policy values.
// It performs no host I/O and confers no catalog or SQL-role authority.
package sepgsql

import (
	"fmt"
	"slices"
	"strings"
)

type SimulationConfig struct {
	Policy              Policy
	InstallSQL          bool
	Permissive          bool
	AuditAllowed        bool
	SuppressDeniedAudit bool
	Audit               func(AuditEvent)
}
type Policy struct {
	ID, Version                      string
	Contexts                         []string
	InitialContext, UnlabeledContext string
	Rules                            []Rule
	CreateTransitions                []CreateTransition
	TrustedTransitions               []TrustedTransition
	PermissiveContexts               []string
	DefaultSpec                      string
	NamedSpecs                       map[string]string
	Translations                     []Translation
}
type Rule struct {
	Source, Target, Class string
	Allow, Deny           []string
}
type CreateTransition struct{ Source, Parent, Class, Name, Result string }
type TrustedTransition struct{ Source, Procedure, Result string }
type Translation struct{ Raw, Translated string }
type AuditEvent struct {
	PolicyID, PolicyVersion      string
	Subject, Target, Class, Name string
	Required, Denied, Audited    []string
	Permissive                   bool
	Message                      string
}

type Mask uint32

const (
	Create Mask = 1 << iota
	Drop
	Getattr
	Setattr
	RelabelFrom
	RelabelTo
)
const (
	Transition Mask = 1 << iota
	Dyntransition
	Setcurrent
)

type classDefinition struct {
	name        string
	number      int
	permissions []string
}

var classes = [...]classDefinition{
	{"process", 0, []string{"transition", "dyntransition", "setcurrent"}},
	{"db_database", 8, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "access", "load_module"}},
	{"db_schema", 9, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "search", "add_name", "remove_name"}},
	{"db_table", 10, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "select", "update", "insert", "delete", "lock", "truncate"}},
	{"db_sequence", 11, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "get_value", "next_value", "set_value"}},
	{"db_procedure", 12, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "execute", "entrypoint", "install"}},
	{"db_column", 13, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "select", "update", "insert"}},
	{"db_view", 17, []string{"create", "drop", "getattr", "setattr", "relabelfrom", "relabelto", "expand"}},
}

func definition(class string) *classDefinition {
	for i := range classes {
		if classes[i].name == class {
			return &classes[i]
		}
	}
	return nil
}
func ClassNumber(class string) int {
	if d := definition(class); d != nil {
		return d.number
	}
	return -1
}
func Permission(class, name string) Mask {
	if d := definition(class); d != nil {
		if i := slices.Index(d.permissions, name); i >= 0 {
			return 1 << i
		}
	}
	return 0
}
func Names(class string, mask Mask) []string {
	var result []string
	if d := definition(class); d != nil {
		for i, p := range d.permissions {
			if mask&(1<<i) != 0 {
				result = append(result, p)
			}
		}
	}
	return result
}

type compiledRule struct {
	source, target, class string
	allow, deny           Mask
}

// Runtime is immutable after Compile. Accessors never lend mutable storage.
type Runtime struct {
	id, version, initial, unlabeled                   string
	contexts, permissiveContexts                      map[string]bool
	rules                                             []compiledRule
	creates                                           []CreateTransition
	trusted                                           []TrustedTransition
	specs                                             map[string][]specRule
	defaultSpec                                       []specRule
	in, out                                           map[string]string
	install, permissive, auditAllowed, suppressDenied bool
	audit                                             func(AuditEvent)
}

func Compile(c SimulationConfig) (*Runtime, error) {
	p := c.Policy
	fail := func(reason string) (*Runtime, error) {
		return nil, fmt.Errorf("stuntgres: invalid sepgsql simulation: %s", reason)
	}
	if p.ID == "" || p.Version == "" || strings.ContainsRune(p.ID+p.Version, 0) {
		return fail("policy ID and Version are required")
	}
	r := &Runtime{id: p.ID, version: p.Version, initial: p.InitialContext, unlabeled: p.UnlabeledContext, contexts: make(map[string]bool), permissiveContexts: make(map[string]bool), specs: make(map[string][]specRule), in: make(map[string]string), out: make(map[string]string), install: c.InstallSQL, permissive: c.Permissive, auditAllowed: c.AuditAllowed, suppressDenied: c.SuppressDeniedAudit, audit: c.Audit}
	for _, context := range p.Contexts {
		if context == "" || context == "*" || strings.ContainsRune(context, 0) || r.contexts[context] {
			return fail("invalid or duplicate context")
		}
		r.contexts[context] = true
	}
	if !r.Valid(r.initial) || !r.Valid(r.unlabeled) {
		return fail("initial and unlabeled contexts must be declared")
	}
	selector := func(s string) bool { return s == "*" || r.Valid(s) }
	for _, rule := range p.Rules {
		if !selector(rule.Source) || !selector(rule.Target) || rule.Class != "*" && definition(rule.Class) == nil {
			return fail("unknown rule selector")
		}
		compileMask := func(ps []string) (Mask, error) {
			var mask Mask
			for _, name := range ps {
				var bit Mask
				if name == "*" {
					if len(ps) != 1 {
						return 0, fmt.Errorf("wildcard permissions must stand alone")
					}
					bit = ^Mask(0)
				} else {
					if rule.Class == "*" {
						return 0, fmt.Errorf("wildcard class requires wildcard permission")
					}
					bit = Permission(rule.Class, name)
				}
				if bit == 0 || mask&bit != 0 {
					return 0, fmt.Errorf("unknown or duplicate permission")
				}
				mask |= bit
			}
			return mask, nil
		}
		allow, err := compileMask(rule.Allow)
		if err != nil {
			return fail(err.Error())
		}
		deny, err := compileMask(rule.Deny)
		if err != nil {
			return fail(err.Error())
		}
		r.rules = append(r.rules, compiledRule{rule.Source, rule.Target, rule.Class, allow, deny})
	}
	for _, context := range p.PermissiveContexts {
		if !r.Valid(context) || r.permissiveContexts[context] {
			return fail("invalid or duplicate permissive context")
		}
		r.permissiveContexts[context] = true
	}
	r.creates = slices.Clone(p.CreateTransitions)
	for i, t := range r.creates {
		if !selector(t.Source) || !selector(t.Parent) || definition(t.Class) == nil || t.Class == "process" || !r.Valid(t.Result) || strings.ContainsRune(t.Name, 0) {
			return fail("invalid create transition")
		}
		for _, u := range r.creates[:i] {
			if t.Class == u.Class && specificity(t.Source, t.Parent, t.Name) == specificity(u.Source, u.Parent, u.Name) && overlaps(t.Source, u.Source) && overlaps(t.Parent, u.Parent) && (t.Name == "" || u.Name == "" || t.Name == u.Name) && t.Result != u.Result {
				return fail("ambiguous create transition")
			}
		}
	}
	r.trusted = slices.Clone(p.TrustedTransitions)
	for i, t := range r.trusted {
		if !selector(t.Source) || !selector(t.Procedure) || !r.Valid(t.Result) {
			return fail("invalid trusted transition")
		}
		for _, u := range r.trusted[:i] {
			if specificity(t.Source, t.Procedure, "") == specificity(u.Source, u.Procedure, "") && overlaps(t.Source, u.Source) && overlaps(t.Procedure, u.Procedure) && t.Result != u.Result {
				return fail("ambiguous trusted transition")
			}
		}
	}
	var err error
	r.defaultSpec, err = r.compileSpec(p.DefaultSpec)
	if err != nil {
		return fail(err.Error())
	}
	for name, text := range p.NamedSpecs {
		if name == "" || strings.ContainsRune(name, 0) {
			return fail("invalid virtual spec name")
		}
		spec, err := r.compileSpec(text)
		if err != nil {
			return fail(err.Error())
		}
		r.specs[name] = spec
	}
	for _, t := range p.Translations {
		if strings.ContainsRune(t.Raw+t.Translated, 0) {
			return fail("invalid translation")
		}
		if _, ok := r.out[t.Raw]; ok {
			return fail("duplicate raw translation")
		}
		if _, ok := r.in[t.Translated]; ok {
			return fail("duplicate translated context")
		}
		r.out[t.Raw], r.in[t.Translated] = t.Translated, t.Raw
	}
	return r, nil
}
func overlaps(a, b string) bool { return a == "*" || b == "*" || a == b }
func specificity(a, b, name string) int {
	n := 0
	if a != "*" {
		n++
	}
	if b != "*" {
		n++
	}
	if name != "" {
		n++
	}
	return n
}
func (r *Runtime) ID() string                { return r.id }
func (r *Runtime) Version() string           { return r.version }
func (r *Runtime) Initial() string           { return r.initial }
func (r *Runtime) Unlabeled() string         { return r.unlabeled }
func (r *Runtime) InstallSQL() bool          { return r.install }
func (r *Runtime) Valid(context string) bool { return r != nil && r.contexts[context] }
func (r *Runtime) Permissive(subject string) bool {
	return r.permissive || r.permissiveContexts[subject]
}
func (r *Runtime) StartupPermissive() bool     { return r.permissive }
func (r *Runtime) AnyTrustedTransitions() bool { return r != nil && len(r.trusted) != 0 }
func (r *Runtime) Translate(text string, inward bool) string {
	m := r.out
	if inward {
		m = r.in
	}
	if translated, ok := m[text]; ok {
		return translated
	}
	return text
}
func (r *Runtime) CreateContext(source, parent, class, name string) string {
	result, best := parent, -1
	for _, t := range r.creates {
		if t.Class == class && overlaps(t.Source, source) && overlaps(t.Parent, parent) && (t.Name == "" || t.Name == name) {
			if score := specificity(t.Source, t.Parent, t.Name); score > best {
				result, best = t.Result, score
			}
		}
	}
	return result
}
func (r *Runtime) TrustedContext(source, procedure string) (string, bool) {
	result, best := source, -1
	for _, t := range r.trusted {
		if overlaps(t.Source, source) && overlaps(t.Procedure, procedure) {
			if score := specificity(t.Source, t.Procedure, ""); score > best {
				result, best = t.Result, score
			}
		}
	}
	return result, best >= 0 && result != source
}

// Decide retains denied bits even when permissive permits the operation.
func (r *Runtime) Decide(subject, target, class, name string, required Mask, debug bool) (bool, *AuditEvent) {
	var allowed, denied Mask
	for _, rule := range r.rules {
		if overlaps(rule.source, subject) && overlaps(rule.target, target) && (rule.class == "*" || rule.class == class) {
			allowed |= rule.allow
			denied |= rule.deny
		}
	}
	denied = required & (^allowed | denied)
	permissive := r.Permissive(subject)
	var audited Mask
	if !r.suppressDenied {
		audited = denied
	}
	if r.auditAllowed {
		audited |= required &^ denied
	}
	if debug {
		audited = required
	}
	if audited == 0 {
		return denied == 0 || permissive, nil
	}
	event := &AuditEvent{PolicyID: r.id, PolicyVersion: r.version, Subject: subject, Target: target, Class: class, Name: name, Required: Names(class, required), Denied: Names(class, denied), Audited: Names(class, audited), Permissive: permissive}
	outcome := "allowed"
	if denied != 0 {
		outcome = "denied"
	}
	event.Message = "SELinux: " + outcome + " { " + strings.Join(event.Audited, " ") + " } scontext=" + subject + " tcontext=" + target + " tclass=" + class
	if name != "" {
		event.Message += fmt.Sprintf(" name=%q", name)
	}
	if denied != 0 {
		if permissive {
			event.Message += " permissive=1"
		} else {
			event.Message += " permissive=0"
		}
	}
	return denied == 0 || permissive, event
}

// Deliver is called only after request/runtime locks and leases are released.
func (r *Runtime) Deliver(event AuditEvent) {
	if r.audit != nil {
		event.Required = slices.Clone(event.Required)
		event.Denied = slices.Clone(event.Denied)
		event.Audited = slices.Clone(event.Audited)
		r.audit(event)
	}
}
