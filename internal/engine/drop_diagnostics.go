package engine

import (
	"cmp"
	"fmt"
	"iter"
	"slices"
	"strings"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Generic drop diagnostics: the dependent labels and RESTRICT/CASCADE
// messages shared by DROP and ALTER TYPE across every feature.

func dependencyObject(view catalog.Read, target ids.ObjectAddress, path []ids.ObjectAddress) string {
	base := target
	base.SubID = 0
	if parent := constraintParent(view, base); parent != nil {
		return "constraint " + catalog.DependencyIdentifier(constraintName(parent, base)) + " on table " + dependencyRelationName(view, parent, path)
	}
	if d, ok := view.Namespace(base); ok {
		return "schema " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := view.Database(base); ok {
		return "database " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := view.Foreign(base); ok {
		if d.Address().ClassID == ids.ForeignMappingClassID {
			user := "public"
			if d.MappedRole != (authorization.PrincipalRef{}) {
				user = catalog.DependencyIdentifier(d.MappedName)
			}
			server := view.Get(d.Server)
			return "user mapping for " + user + " on server " + catalog.DependencyIdentifier(server.Name())
		}
		return fdw.Label(d.Address().ClassID) + " " + catalog.DependencyIdentifier(d.Name())
	}
	if d, ok := view.Extension(base); ok {
		return "extension " + catalog.DependencyIdentifier(d.Name())
	}
	if _, ok := view.Operator(base); ok {
		return "operator " + regobj.OperatorSpelling(view, path, regobj.CollectOperatorGroups(view), base.ObjectID, sqltype.Regoperator)
	}
	if d, ok := view.Procedure(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		visible := path == nil
		for _, namespace := range path {
			if n, ok := view.Namespace(namespace); ok && n.Temporary != (ids.SessionToken{}) {
				continue
			}
			if address, ok := view.LookupName(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: namespace, Name: d.Name(), Signature: string(d.Signature)}); ok {
				visible = address == d.Address()
				break
			}
		}
		if !visible {
			name = catalog.DependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
		}
		var text strings.Builder
		text.WriteString("function ")
		text.WriteString(name)
		text.WriteByte('(')
		regobj.WriteRoutineArguments(&text, view, path, d.Signature, true)
		text.WriteByte(')')
		return text.String()
	}
	if d, ok := view.TSDictionary(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		visible := path == nil
		for _, ns := range path {
			if n, ok := view.Namespace(ns); ok && n.Temporary != (ids.SessionToken{}) {
				continue
			}
			if address, ok := view.LookupName(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ns, Name: d.Name()}); ok {
				visible = address == d.Address()
				break
			}
		}
		if !visible {
			name = catalog.DependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
		}
		return "text search dictionary " + name
	}
	if t, ok := view.Type(base); ok {
		return "type " + catalog.TypeDiagnosticNameAt(view, t, path)
	}
	if d, ok := view.Collation(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := view.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
					continue
				}
				if a, ok := view.LookupName(ids.NameKey{ClassID: ids.CollationClassID, Namespace: ns, Name: d.Name()}); ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = catalog.DependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
			}
		}
		return "collation " + name
	}
	if d, ok := view.Conversion(base); ok {
		name := catalog.DependencyIdentifier(d.Name())
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := view.Namespace(ns); ok && namespace.Temporary != (ids.SessionToken{}) {
					continue
				}
				if a, ok := view.LookupName(ids.NameKey{ClassID: ids.ConversionClassID, Namespace: ns, Name: d.Name()}); ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = catalog.DependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
			}
		}
		return "conversion " + name
	}
	d := view.MustRelation(base)
	if d.SQLIndex != nil && d.SQLIndex.Constraint != nil {
		parent := view.MustRelation(d.SQLIndex.Parent)
		return "constraint " + catalog.DependencyIdentifier(d.SQLIndex.Constraint.Name) + " on table " + dependencyRelationName(view, parent, path)
	}
	object := relationDiagnosticKind(d) + " " + dependencyRelationName(view, d, path)
	if target.SubID != 0 {
		for i, attribute := range d.Attributes {
			if attribute == target.SubID {
				return "column " + d.Columns[i].Name + " of " + object
			}
		}
	}
	return object
}

func dependencyRelationName(catalogView catalog.Read, d *catalog.Relation, path []ids.ObjectAddress) string {
	// Internal provider stages have no SQL visibility context. SQL utilities
	// supply their actual lookup path, including an empty effective path.
	if path == nil {
		return catalog.DependencyIdentifier(d.Name())
	}
	return stored.RelationDiagnosticNameAt(catalogView, d, path)
}

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
func supportedDependentLabels(catalog catalog.Read, targets []ids.ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ids.ObjectAddress, foreign ...domainDependency) []supportedDependent {
	policyTargets := targets
	if len(views) != 0 {
		policyTargets = make([]ids.ObjectAddress, len(targets), len(targets)+len(views))
		copy(policyTargets, targets)
		for _, edge := range views {
			policyTargets = append(policyTargets, edge.view.Address())
		}
	}
	dependents := policyDependentLabels(catalog, policyTargets, path)
	dependents = append(dependents, ruleDependentLabels(catalog, policyTargets, path)...)
	return supportedDependentLabelsWithPolicies(catalog, targets, checks, views, defaults, path, dependents, foreign...)
}

// Other labels are captured before CASCADE mutates policy and rule metadata.
func supportedDependentLabelsWithPolicies(catalogView catalog.Read, targets []ids.ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ids.ObjectAddress, other []supportedDependent, foreign ...domainDependency) []supportedDependent {
	dependents := make([]supportedDependent, 0, len(checks)+len(views)+len(defaults)+len(other)+len(foreign))
	dependents = append(dependents, other...)
	for _, edge := range defaults {
		label := "default value for column " + catalog.DependencyIdentifier(edge.column) + " of table " + dependencyRelationName(catalogView, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.relation.ColumnMeta.Defaults[edge.attribute].OID, target: edge.target, label: label})
	}
	for _, edge := range checks {
		label := "constraint " + catalog.DependencyIdentifier(edge.check.Name) + " on table " + dependencyRelationName(catalogView, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.check.OID, target: edge.target, label: label, checkOwner: edge.relation.Address().ObjectID, checkOrdinal: edge.check.Ordinal})
	}
	for _, edge := range views {
		dependents = append(dependents, supportedDependent{oid: edge.view.Address().ObjectID, target: edge.target, view: edge.view.Address(), label: relationDiagnosticKind(edge.view) + " " + dependencyRelationName(catalogView, edge.view, path)})
	}
	for _, edge := range foreign {
		if !edge.automatic {
			dependents = append(dependents, supportedDependent{oid: edge.oid, target: edge.target, label: edge.label})
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

func supportedRestrictErrorLabels(catalog catalog.Read, views []viewDependencyEdge, path []ids.ObjectAddress, dependents []supportedDependent, multipleDeletion bool) error {
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		provider := dependencyObject(catalog, dependent.target, path)
		// The dependency targets the physical supporting index. Its owning
		// constraint identifies the requested DROP in Message, not this edge.
		if index, ok := catalog.Relation(dependent.target); ok && index.SQLIndex != nil {
			provider = "index " + dependencyRelationName(catalog, index, path)
		}
		details[i] = dependent.label + " depends on " + provider
	}
	// Closure edges are parent-first. Walk backwards to keep the original
	// requested provider in Message while Detail retains each actual edge.
	message := "cannot drop desired object(s) because other objects depend on them"
	if !multipleDeletion {
		target := dependents[0].target
		for i := len(views) - 1; i >= 0; i-- {
			if views[i].view.Address() == target {
				target = views[i].target
			}
		}
		message = "cannot drop " + dependencyObject(catalog, target, path) + " because other objects depend on it"
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

func ruleDependentLabelsFrom(view catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor], observe func(ids.ObjectAddress)) []supportedDependent {
	var dependents []supportedDependent
	for _, descriptor := range descriptors {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || slices.Contains(targets, d.Address()) {
			continue
		}
		for _, r := range rulesOf(d) {
			for _, target := range targets {
				if ruleDepends(view, r, target) {
					if observe != nil {
						observe(d.Address())
					}
					dependents = append(dependents, supportedDependent{oid: r.OID, target: target, label: "rule " + catalog.DependencyIdentifier(r.Name) + " on " + relationDiagnosticKind(d) + " " + dependencyRelationName(view, d, path)})
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

func policyDependentLabelsFrom(view catalog.Read, targets []ids.ObjectAddress, path []ids.ObjectAddress, descriptors iter.Seq2[ids.ObjectAddress, catalog.Descriptor]) []supportedDependent {
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
				if policyDepends(p, target) {
					dependents = append(dependents, supportedDependent{oid: p.OID, target: target, label: "policy " + catalog.DependencyIdentifier(p.Name) + " on table " + dependencyRelationName(view, d, path)})
					break
				}
			}
		}
	}
	return dependents
}
