package view

import (
	"fmt"
	"slices"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// DependencyClosure returns the views that depend on targets, directly or
// through other views, each with the target it was reached from, depth
// first. views are the live views in OID order; a whole-relation target is
// never its own dependent.
func DependencyClosure(views []*catalog.Relation, targets []ids.ObjectAddress) []DependencyEdge {
	seen := make(map[ids.ObjectAddress]bool, len(targets))
	for _, target := range targets {
		if target.SubID == 0 {
			seen[target] = true
		}
	}
	var edges []DependencyEdge
	var visit func(ids.ObjectAddress)
	visit = func(target ids.ObjectAddress) {
		for _, viewRelation := range views {
			if !seen[viewRelation.Address()] && ViewDepends(viewRelation, target) {
				seen[viewRelation.Address()] = true
				edges = append(edges, DependencyEdge{View: viewRelation, Target: target})
				visit(viewRelation.Address())
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return edges
}

// KeepRules returns the rules that depend on none of targets, in order.
func KeepRules(base catalog.Read, rules []stored.Rule, targets []ids.ObjectAddress) []stored.Rule {
	var kept []stored.Rule
	for _, r := range rules {
		drop := false
		for _, target := range targets {
			if RuleDepends(base, r, target) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, r)
		}
	}
	return kept
}

// KeepPolicies returns the policies that depend on none of targets, in order.
func KeepPolicies(policies []catalog.PolicyDefinition, targets []ids.ObjectAddress) []catalog.PolicyDefinition {
	var kept []catalog.PolicyDefinition
	for _, p := range policies {
		drop := false
		for _, target := range targets {
			if PolicyDepends(p, target) {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, p)
		}
	}
	return kept
}

// DependentViews is DependencyClosure over the live views of r, in OID order.
func DependentViews(r DropReader, targets []ids.ObjectAddress) []DependencyEdge {
	var views []*catalog.Relation
	for address, descriptor := range r.Descriptors() {
		if r.Removed(address) {
			continue
		}
		if replacement := r.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && d.View != nil {
			views = append(views, d)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	return DependencyClosure(views, targets)
}

// DropPolicyDependents removes, under CASCADE, the policies of other
// relations in r that depend on targets.
func DropPolicyDependents(r DropReader, targets []ids.ObjectAddress, mode parser.DropMode) error {
	if mode == parser.DropRestrict {
		return nil
	}
	for address, descriptor := range r.InputDescriptors() {
		if r.Removed(address) || slices.Contains(targets, address) {
			continue
		}
		if replacement := r.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Security.Policies) == 0 {
			continue
		}
		kept := KeepPolicies(d.Security.Policies, targets)
		if len(kept) == len(d.Security.Policies) {
			continue
		}
		copy := *d
		copy.Security.Policies = kept
		revision, err := r.Revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err = r.Replace(&copy); err != nil {
			return err
		}
	}
	return nil
}

// DropRuleDependents removes, under CASCADE, the rules of other relations in
// r that depend on targets.
func DropRuleDependents(r DropReader, targets []ids.ObjectAddress, mode parser.DropMode) error {
	if mode == parser.DropRestrict {
		return nil
	}
	for address, descriptor := range r.InputDescriptors() {
		if r.Removed(address) || slices.Contains(targets, address) {
			continue
		}
		if replacement := r.Replacement(address); replacement != nil {
			descriptor = replacement
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(stored.RulesOf(d)) == 0 && !d.HasTriggers() {
			continue
		}
		kept := KeepRules(r.BaseRead(), stored.RulesOf(d), targets)
		keptTriggers := stored.KeepTriggers(stored.TriggersOf(d), targets)
		if len(kept) == len(stored.RulesOf(d)) && len(keptTriggers) == len(stored.TriggersOf(d)) {
			continue
		}
		copy := *d
		copy.Rules = stored.RulesPayload(kept)
		copy.Triggers = stored.TriggersPayload(keptTriggers)
		revision, err := r.Revision(copy.Revision())
		if err != nil {
			return err
		}
		copy.ObjectRevision = revision
		if err := r.Replace(&copy); err != nil {
			return err
		}
	}
	return nil
}

// TypeChangeError rejects ALTER COLUMN TYPE on column, at target, when a rule
// or a view of r depends on it, naming the first such dependent. r's path
// qualifies a view name that is not visible on it.
func TypeChangeError(r DropReader, target ids.ObjectAddress, column string) error {
	base, path := r.BaseRead(), r.Path()
	if dependents := RuleDependentLabels(base, []ids.ObjectAddress{target}, path); len(dependents) != 0 {
		d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`%s depends on column "%s"`, dependents[0].Label, column)
		return d
	}
	edges := DependentViews(r, []ids.ObjectAddress{target})
	if len(edges) == 0 {
		return nil
	}
	dependent := edges[0].View
	name := dependent.Name()
	if parser.IdentifierNeedsQuotes(name) {
		name = catalog.QuoteIdentifier(name)
	}
	namespace := base.Get(dependent.Namespace()).Name()
	visible := false
	for _, schema := range path {
		found := base.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: schema, Name: dependent.Name()})
		if len(found) != 0 {
			visible = len(found) == 1 && found[0] == dependent.Address()
			break
		}
	}
	if !visible {
		if parser.IdentifierNeedsQuotes(namespace) {
			namespace = catalog.QuoteIdentifier(namespace)
		}
		name = namespace + "." + name
	}
	d := sqlError("0A000", "cannot alter type of a column used by a view or rule").(*sqlstate.Error)
	d.Detail = fmt.Sprintf(`rule _RETURN on view %s depends on column "%s"`, name, column)
	return d
}
