package view

import (
	"cmp"
	"fmt"
	"iter"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

// Generic drop diagnostics: the dependent labels and RESTRICT/CASCADE
// messages shared by DROP and ALTER TYPE across every feature.

// Dependent is one dependent object a DROP or ALTER TYPE reports: its
// identity for ordering, the target it depends on and its diagnostic label.
type Dependent struct {
	OID          ids.OID
	target       ids.ObjectAddress
	view         ids.ObjectAddress
	Label        string
	checkOwner   ids.OID
	checkOrdinal int
}

// DependentLabels labels the policy, rule, default, CHECK, view and foreign
// dependents of targets. Walk the existing immediate-provider edges
// depth-first. Genuine identities order mixed siblings; same-owner CHECK
// siblings retain declaration order.
func DependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, checks []table.CheckDependencyEdge, views []DependencyEdge, defaults []table.DefaultDependencyEdge, path []ids.ObjectAddress, foreign ...table.DomainDependency) []Dependent {
	policyTargets := targets
	if len(views) != 0 {
		policyTargets = make([]ids.ObjectAddress, len(targets), len(targets)+len(views))
		copy(policyTargets, targets)
		for _, edge := range views {
			policyTargets = append(policyTargets, edge.View.Address())
		}
	}
	dependents := PolicyDependentLabels(catalog, policyTargets, path)
	dependents = append(dependents, RuleDependentLabels(catalog, policyTargets, path)...)
	return DependentLabelsWithPolicies(catalog, targets, checks, views, defaults, path, dependents, foreign...)
}

// DependentLabelsWithPolicies is DependentLabels with the policy and rule
// labels in other. Other labels are captured before CASCADE mutates policy
// and rule metadata.
func DependentLabelsWithPolicies(catalogView catalog.Read, targets []ids.ObjectAddress, checks []table.CheckDependencyEdge, views []DependencyEdge, defaults []table.DefaultDependencyEdge, path []ids.ObjectAddress, other []Dependent, foreign ...table.DomainDependency) []Dependent {
	dependents := make([]Dependent, 0, len(checks)+len(views)+len(defaults)+len(other)+len(foreign))
	dependents = append(dependents, other...)
	for _, edge := range defaults {
		label := "default value for column " + catalog.DependencyIdentifier(edge.Column) + " of table " + stored.DependencyRelationName(catalogView, edge.Relation, path)
		dependents = append(dependents, Dependent{OID: edge.Relation.ColumnMeta.Defaults[edge.Attribute].OID, target: edge.Target, Label: label})
	}
	for _, edge := range checks {
		label := "constraint " + catalog.DependencyIdentifier(edge.Check.Name) + " on table " + stored.DependencyRelationName(catalogView, edge.Relation, path)
		dependents = append(dependents, Dependent{OID: edge.Check.OID, target: edge.Target, Label: label, checkOwner: edge.Relation.Address().ObjectID, checkOrdinal: edge.Check.Ordinal})
	}
	for _, edge := range views {
		dependents = append(dependents, Dependent{OID: edge.View.Address().ObjectID, target: edge.Target, view: edge.View.Address(), Label: catalog.RelationDiagnosticKind(edge.View) + " " + stored.DependencyRelationName(catalogView, edge.View, path)})
	}
	for _, edge := range foreign {
		if !edge.Automatic {
			dependents = append(dependents, Dependent{OID: edge.OID, target: edge.Target, Label: edge.Label})
		}
	}
	slices.SortFunc(dependents, func(a, b Dependent) int { return cmp.Compare(a.OID, b.OID) })
	// CHECK OIDs can be assigned in enforcement/name order. Permute only their
	// occupied sibling slots, leaving every non-CHECK position and provider edge
	// intact. A mixed ordinal/OID comparator would not be transitive.
	for i := range dependents {
		if dependents[i].checkOwner == 0 {
			continue
		}
		first := i
		for j := i + 1; j < len(dependents); j++ {
			if dependents[j].checkOwner == dependents[i].checkOwner && dependents[j].target == dependents[i].target && dependents[j].checkOrdinal < dependents[first].checkOrdinal {
				first = j
			}
		}
		dependents[i], dependents[first] = dependents[first], dependents[i]
	}
	if len(views) == 0 && len(targets) <= 1 {
		return dependents
	}
	ordered := make([]Dependent, 0, len(dependents))
	seen := make([]bool, len(dependents))
	var visit func(ids.ObjectAddress)
	visit = func(target ids.ObjectAddress) {
		for i, dependent := range dependents {
			if seen[i] || dependent.target.ClassID != target.ClassID || dependent.target.ObjectID != target.ObjectID || target.SubID != 0 && dependent.target.SubID != target.SubID {
				continue
			}
			seen[i] = true
			ordered = append(ordered, dependent)
			if dependent.view != (ids.ObjectAddress{}) {
				visit(dependent.view)
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return ordered
}

// DependentsRestrictError is the RESTRICT error for dependents, naming the
// requested provider unless the drop removes several objects.
func DependentsRestrictError(catalog catalog.Read, views []DependencyEdge, path []ids.ObjectAddress, dependents []Dependent, multipleDeletion bool) error {
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		provider := stored.DependencyObject(catalog, dependent.target, path)
		// The dependency targets the physical supporting index. Its owning
		// constraint identifies the requested DROP in Message, not this edge.
		if index, ok := catalog.Relation(dependent.target); ok && index.SQLIndex != nil {
			provider = "index " + stored.DependencyRelationName(catalog, index, path)
		}
		details[i] = dependent.Label + " depends on " + provider
	}
	// Closure edges are parent-first. Walk backwards to keep the original
	// requested provider in Message while Detail retains each actual edge.
	message := "cannot drop desired object(s) because other objects depend on them"
	if !multipleDeletion {
		target := dependents[0].target
		for i := len(views) - 1; i >= 0; i-- {
			if views[i].View.Address() == target {
				target = views[i].Target
			}
		}
		message = "cannot drop " + stored.DependencyObject(catalog, target, path) + " because other objects depend on it"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

// EmitCascade emits the CASCADE NOTICE that lists details, folding
// several into one count.
func EmitCascade(op *session.Operation, details []string) {
	if len(details) == 0 {
		return
	}
	message, detail := details[0], ""
	if len(details) > 1 {
		message = fmt.Sprintf("drop cascades to %d other objects", len(details))
		detail = strings.Join(details, "\n")
	}
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
}

// RuleDependentLabels labels the rules of other relations that depend on
// targets.
func RuleDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress) []Dependent {
	return RuleDependentLabelsFrom(catalog, targets, path, catalog.RelationDescriptors(), nil)
}

// RuleDependentLabelsFrom is RuleDependentLabels over descriptors, calling
// observe, when set, for each relation with a dependent rule.
func RuleDependentLabelsFrom(catalogView catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], observe func(ids.ObjectAddress)) []Dependent {
	var dependents []Dependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || slices.Contains(targets, d.Address()) {
			continue
		}
		for _, r := range stored.RulesOf(d) {
			for _, target := range targets {
				if RuleDepends(catalogView, r, target) {
					if observe != nil {
						observe(d.Address())
					}
					dependents = append(dependents, Dependent{OID: r.OID, target: target, Label: "rule " + catalog.DependencyIdentifier(r.Name) + " on " + catalog.RelationDiagnosticKind(d) + " " + stored.DependencyRelationName(catalogView, d, path)})
					break
				}
			}
		}
		for _, trigger := range stored.TriggersOf(d) {
			for _, target := range targets {
				if stored.TriggerDepends(trigger, target) {
					if observe != nil {
						observe(d.Address())
					}
					dependents = append(dependents, Dependent{OID: trigger.OID(), target: target, Label: "trigger " + catalog.DependencyIdentifier(trigger.Name()) + " on table " + stored.DependencyRelationName(catalogView, d, path)})
					break
				}
			}
		}
	}
	return dependents
}

// PolicyDependentLabels labels the policies of other relations that depend
// on targets.
func PolicyDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress) []Dependent {
	return PolicyDependentLabelsFrom(catalog, targets, path, catalog.RelationDescriptors())
}

// PolicyDependentLabelsFrom is PolicyDependentLabels over descriptors.
func PolicyDependentLabelsFrom(catalogView catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []Dependent {
	var dependents []Dependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if slices.Contains(targets, d.Address()) {
			continue
		}
		for _, p := range d.Security.Policies {
			for _, target := range targets {
				if PolicyDepends(p, target) {
					dependents = append(dependents, Dependent{OID: p.OID, target: target, Label: "policy " + catalog.DependencyIdentifier(p.Name) + " on table " + stored.DependencyRelationName(catalogView, d, path)})
					break
				}
			}
		}
	}
	return dependents
}
