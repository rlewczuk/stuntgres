package view

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
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
