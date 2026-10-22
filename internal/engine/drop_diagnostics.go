package engine

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
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqlstate"
)

// Generic drop diagnostics: the dependent labels and RESTRICT/CASCADE
// messages shared by DROP and ALTER TYPE across every feature.

type supportedDependent struct {
	oid          ids.OID
	target       ids.ObjectAddress
	view         ids.ObjectAddress
	label        string
	checkOwner   ids.OID
	checkOrdinal int
}

// Walk the existing immediate-provider edges depth-first. Genuine identities
// order mixed siblings; same-owner CHECK siblings retain declaration order.
func supportedDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, checks []table.CheckDependencyEdge, views []view.DependencyEdge, defaults []table.DefaultDependencyEdge, path []ids.ObjectAddress, foreign ...table.DomainDependency) []supportedDependent {
	policyTargets := targets
	if len(views) != 0 {
		policyTargets = make([]ids.ObjectAddress, len(targets), len(targets)+len(views))
		copy(policyTargets, targets)
		for _, edge := range views {
			policyTargets = append(policyTargets, edge.View.Address())
		}
	}
	dependents := policyDependentLabels(catalog, policyTargets, path)
	dependents = append(dependents, ruleDependentLabels(catalog, policyTargets, path)...)
	return supportedDependentLabelsWithPolicies(catalog, targets, checks, views, defaults, path, dependents, foreign...)
}

// Other labels are captured before CASCADE mutates policy and rule metadata.
func supportedDependentLabelsWithPolicies(catalogView catalog.Read, targets []ids.ObjectAddress, checks []table.CheckDependencyEdge, views []view.DependencyEdge, defaults []table.DefaultDependencyEdge, path []ids.ObjectAddress, other []supportedDependent, foreign ...table.DomainDependency) []supportedDependent {
	dependents := make([]supportedDependent, 0, len(checks)+len(views)+len(defaults)+len(other)+len(foreign))
	dependents = append(dependents, other...)
	for _, edge := range defaults {
		label := "default value for column " + catalog.DependencyIdentifier(edge.Column) + " of table " + stored.DependencyRelationName(catalogView, edge.Relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.Relation.ColumnMeta.Defaults[edge.Attribute].OID, target: edge.Target, label: label})
	}
	for _, edge := range checks {
		label := "constraint " + catalog.DependencyIdentifier(edge.Check.Name) + " on table " + stored.DependencyRelationName(catalogView, edge.Relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.Check.OID, target: edge.Target, label: label, checkOwner: edge.Relation.Address().ObjectID, checkOrdinal: edge.Check.Ordinal})
	}
	for _, edge := range views {
		dependents = append(dependents, supportedDependent{oid: edge.View.Address().ObjectID, target: edge.Target, view: edge.View.Address(), label: catalog.RelationDiagnosticKind(edge.View) + " " + stored.DependencyRelationName(catalogView, edge.View, path)})
	}
	for _, edge := range foreign {
		if !edge.Automatic {
			dependents = append(dependents, supportedDependent{oid: edge.OID, target: edge.Target, label: edge.Label})
		}
	}
	slices.SortFunc(dependents, func(a, b supportedDependent) int { return cmp.Compare(a.oid, b.oid) })
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
	ordered := make([]supportedDependent, 0, len(dependents))
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

func supportedRestrictErrorLabels(catalog catalog.Read, views []view.DependencyEdge, path []ids.ObjectAddress, dependents []supportedDependent, multipleDeletion bool) error {
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		provider := stored.DependencyObject(catalog, dependent.target, path)
		// The dependency targets the physical supporting index. Its owning
		// constraint identifies the requested DROP in Message, not this edge.
		if index, ok := catalog.Relation(dependent.target); ok && index.SQLIndex != nil {
			provider = "index " + stored.DependencyRelationName(catalog, index, path)
		}
		details[i] = dependent.label + " depends on " + provider
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

func emitViewCascade(op *session.Operation, details []string) {
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

func ruleDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress) []supportedDependent {
	return ruleDependentLabelsFrom(catalog, targets, path, catalog.Descriptors(), nil)
}

func ruleDependentLabelsFrom(catalogView catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], observe func(ids.ObjectAddress)) []supportedDependent {
	var dependents []supportedDependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || slices.Contains(targets, d.Address()) {
			continue
		}
		for _, r := range stored.RulesOf(d) {
			for _, target := range targets {
				if view.RuleDepends(catalogView, r, target) {
					if observe != nil {
						observe(d.Address())
					}
					dependents = append(dependents, supportedDependent{oid: r.OID, target: target, label: "rule " + catalog.DependencyIdentifier(r.Name) + " on " + catalog.RelationDiagnosticKind(d) + " " + stored.DependencyRelationName(catalogView, d, path)})
					break
				}
			}
		}
	}
	return dependents
}

func policyDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress) []supportedDependent {
	return policyDependentLabelsFrom(catalog, targets, path, catalog.Descriptors())
}

func policyDependentLabelsFrom(catalogView catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []supportedDependent {
	var dependents []supportedDependent
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
				if view.PolicyDepends(p, target) {
					dependents = append(dependents, supportedDependent{oid: p.OID, target: target, label: "policy " + catalog.DependencyIdentifier(p.Name) + " on table " + stored.DependencyRelationName(catalogView, d, path)})
					break
				}
			}
		}
	}
	return dependents
}
