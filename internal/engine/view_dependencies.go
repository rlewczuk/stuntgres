package engine

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func viewDepends(d *relationDescriptor, target ObjectAddress) bool {
	if d.view == nil {
		return false
	}
	for _, leaf := range d.view.sources {
		if target.SubID == 0 && leaf.ref.Address == target {
			return true
		}
	}
	for _, dependency := range d.view.dependencies {
		a := dependency.ref.Address
		if a.ClassID == target.ClassID && a.ObjectID == target.ObjectID && (target.SubID == 0 || a.SubID == target.SubID) {
			return true
		}
	}
	return false
}

type viewDependencyEdge struct {
	view   *relationDescriptor
	target ObjectAddress
}

func viewDependencyClosure(catalog CatalogRead, targets []ObjectAddress, stage *CatalogStage) []viewDependencyEdge {
	var views []*relationDescriptor
	collect := func(address ObjectAddress, descriptor Descriptor) {
		if stage != nil {
			if _, removed := stage.removals[address]; removed {
				return
			}
			if replacement, changed := stage.descriptors[address]; changed {
				descriptor = replacement
			}
		}
		if d, ok := descriptor.(*relationDescriptor); ok && d.view != nil {
			views = append(views, d)
		}
	}
	for address, descriptor := range catalog.state.descriptors {
		collect(address, descriptor)
	}
	if stage != nil {
		for address, descriptor := range stage.descriptors {
			if _, existing := catalog.state.descriptors[address]; !existing {
				collect(address, descriptor)
			}
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].address.ObjectID < views[j].address.ObjectID })
	seen := make(map[ObjectAddress]bool, len(targets))
	for _, target := range targets {
		if target.SubID == 0 {
			seen[target] = true
		}
	}
	var edges []viewDependencyEdge
	var visit func(ObjectAddress)
	visit = func(target ObjectAddress) {
		for _, view := range views {
			if !seen[view.address] && viewDepends(view, target) {
				seen[view.address] = true
				edges = append(edges, viewDependencyEdge{view, target})
				visit(view.address)
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return edges
}

func dependencyObject(catalog CatalogRead, target ObjectAddress) string {
	base := target
	base.SubID = 0
	d := catalog.state.descriptors[base].(*relationDescriptor)
	object := relationDiagnosticKind(d) + " " + d.name
	if target.SubID != 0 {
		for i, attribute := range d.attributes {
			if attribute == target.SubID {
				return "column " + d.columns[i].Name + " of " + object
			}
		}
	}
	return object
}

func supportedRestrictError(catalog CatalogRead, checks []checkDependencyEdge, views []viewDependencyEdge) error {
	// CHECKs have owner-local declaration identities; views use their existing
	// relation creation addresses. Neither family takes precedence over another.
	type dependent struct {
		oid     OID
		ordinal int
		target  ObjectAddress
		detail  string
	}
	dependents := make([]dependent, 0, len(checks)+len(views))
	for _, edge := range checks {
		dependents = append(dependents, dependent{edge.relation.address.ObjectID, edge.check.ordinal, edge.target, "constraint " + edge.check.Name + " on table " + edge.relation.name + " depends on " + dependencyObject(catalog, edge.target)})
	}
	for _, edge := range views {
		dependents = append(dependents, dependent{edge.view.address.ObjectID, 0, edge.target, "view " + edge.view.name + " depends on " + dependencyObject(catalog, edge.target)})
	}
	slices.SortFunc(dependents, func(a, b dependent) int {
		if order := cmp.Compare(a.oid, b.oid); order != 0 {
			return order
		}
		return cmp.Compare(a.ordinal, b.ordinal)
	})
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		details[i] = dependent.detail
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + dependencyObject(catalog, dependents[0].target) + " because other objects depend on it", Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func stageViewDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	checks, err := stageCheckDependents(stage, targets, mode)
	if err != nil {
		return err
	}
	edges := viewDependencyClosure(stage.base, targets, stage)
	if len(edges) == 0 && len(checks) == 0 {
		return nil
	}
	if mode == parser.DropRestrict {
		return supportedRestrictError(stage.base, checks, edges)
	}
	for _, edge := range edges {
		d := edge.view
		if _, exists := stage.removals[d.address]; exists {
			continue
		}
		if err := stage.Remove(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: d.address}); err != nil {
			return err
		}
		stage.viewCascadeNames = append(stage.viewCascadeNames, "drop cascades to view "+d.name)
	}
	return nil
}

func emitViewCascade(op *Operation, details []string) {
	if len(details) == 0 {
		return
	}
	message, detail := details[0], ""
	if len(details) > 1 {
		message = fmt.Sprintf("drop cascades to %d other objects", len(details))
		detail = strings.Join(details, "\n")
	}
	op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
}

func validateViewDefinition(v CatalogRead, d *relationDescriptor) error {
	definition := d.view
	if definition == nil || definition.syntax == nil || definition.query == nil || definition.query.Root == nil || definition.rule == 0 || len(definition.query.Root.Schema()) != len(d.columns) {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	if err := definition.validateLiterals(v); err != nil {
		return err
	}
	if err := validateViewLeaves(v, definition); err != nil {
		return err
	}
	if err := definition.validateNamespaces(); err != nil {
		return err
	}
	for _, leaf := range definition.sources {
		source, ok := v.state.descriptors[leaf.ref.Address].(*relationDescriptor)
		if !ok || leaf.ref.owner != v.owner.owner || leaf.ref.Epoch != v.Epoch() || leaf.ref.Address.SubID != 0 || source.address == d.address {
			return fmt.Errorf("stuntgres: invalid view source identity")
		}
		namespace, ok := v.state.descriptors[source.namespace].(*namespaceDescriptor)
		if !ok || namespace.temporary != (SessionToken{}) {
			return fmt.Errorf("stuntgres: invalid view source lifetime")
		}
		for _, attribute := range leaf.reads {
			frozen := false
			for _, id := range leaf.attributes {
				frozen = frozen || id == attribute
			}
			if !frozen {
				return fmt.Errorf("stuntgres: invalid view read attribute")
			}
			if !source.hasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	active, visited := make(map[ObjectAddress]bool), make(map[ObjectAddress]bool)
	var visit func(*relationDescriptor) error
	visit = func(source *relationDescriptor) error {
		if active[source.address] {
			return fmt.Errorf("stuntgres: cyclic view source")
		}
		if visited[source.address] || source.view == nil {
			return nil
		}
		active[source.address] = true
		for _, leaf := range source.view.sources {
			next, ok := v.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok {
				return fmt.Errorf("stuntgres: missing view source identity")
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		delete(active, source.address)
		visited[source.address] = true
		return nil
	}
	if err := visit(d); err != nil {
		return err
	}
	for _, dependency := range definition.dependencies {
		ref := dependency.ref
		address := ref.Address
		address.SubID = 0
		if ref.owner != v.owner.owner || ref.Epoch != v.Epoch() {
			return fmt.Errorf("stuntgres: invalid view dependency identity")
		}
		object, ok := v.state.descriptors[address]
		if !ok {
			return fmt.Errorf("stuntgres: missing view dependency")
		}
		if ref.Address.SubID != 0 {
			relation, ok := object.(*relationDescriptor)
			if !ok || !relation.hasAttribute(ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	return nil
}

func validateViewLeaves(catalog CatalogRead, definition *viewDefinition) error {
	occurrence := 0
	marked := false
	var visit func(LogicalNode) error
	visit = func(node LogicalNode) error {
		for _, column := range node.Schema() {
			if !column.Usage.Valid() {
				return fmt.Errorf("stuntgres: invalid view query usage")
			}
			typ, ok := catalog.LookupType(column.Usage.Type.ID())
			if !ok || typ != column.Usage.Type {
				return fmt.Errorf("stuntgres: foreign view query type")
			}
		}
		switch n := node.(type) {
		case *limitNode:
			if definition.syntax.Bounds == nil {
				return fmt.Errorf("stuntgres: unexpected stored view bounds")
			}
			for _, expr := range []BoundExpr{n.offset, n.count} {
				if expr != nil && (expr.ResultUsage().Type.ID() != sqltype.Int8 || expr.Properties().RowDependent || expr.Properties().StageDependent) {
					return fmt.Errorf("stuntgres: invalid stored view bound")
				}
			}
			return visit(n.child)
		case *evaluationNode:
			return visit(n.child)
		case *sourceNode:
			if occurrence >= len(definition.sources) || n.occurrence != occurrence {
				return fmt.Errorf("stuntgres: invalid view source occurrence")
			}
			leaf := definition.sources[occurrence]
			occurrence++
			source, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok || n.ref != leaf.ref || len(n.Schema()) != len(leaf.attributes) {
				return fmt.Errorf("stuntgres: invalid view source layout")
			}
			for slot, attribute := range leaf.attributes {
				for current, live := range source.attributes {
					if live == attribute && n.Schema()[slot].Usage != source.columns[current].Usage {
						return fmt.Errorf("stuntgres: invalid view source usage")
					}
				}
			}
		case *joinNode:
			if n.leftWidth != len(n.left.Schema()) || n.rightWidth != len(n.right.Schema()) || len(n.Schema()) != n.leftWidth+n.rightWidth+len(n.merges) {
				return fmt.Errorf("stuntgres: invalid view join frame")
			}
			for i, merge := range n.merges {
				if merge == nil || merge.ResultUsage() != n.Schema()[n.leftWidth+n.rightWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid view merged column usage")
				}
			}
			if err := visit(n.left); err != nil {
				return err
			}
			return visit(n.right)
		case *rowLockNode:
			if marked || definition.syntax.Locking == nil || queryRowClause(n.mode) == "" || queryRowClause(n.mode) != definition.syntax.Locking.Mode {
				return fmt.Errorf("stuntgres: invalid stored view rowmark")
			}
			marked = true
			return visit(n.child)
		case *filterNode:
			return visit(n.child)
		case *sortNode:
			return visit(n.child)
		case *projectNode:
			return visit(n.child)
		case *aggregateNode:
			return visit(n.child)
		case *singletonNode:
		default:
			return fmt.Errorf("stuntgres: invalid view query node %T", node)
		}
		return nil
	}
	if err := visit(definition.query.Root); err != nil {
		return err
	}
	if occurrence != len(definition.sources) {
		return fmt.Errorf("stuntgres: invalid view source manifest")
	}
	if definition.syntax.Locking != nil && !marked {
		return fmt.Errorf("stuntgres: missing stored view rowmark")
	}
	return nil
}
