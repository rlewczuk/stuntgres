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

func dependencyObject(catalog CatalogRead, target ObjectAddress, path []ObjectAddress) string {
	base := target
	base.SubID = 0
	if t, ok := catalog.state.descriptors[base].(*typeDescriptor); ok {
		return "type " + typeDiagnosticNameAt(catalog, t, path)
	}
	d := catalog.state.descriptors[base].(*relationDescriptor)
	object := relationDiagnosticKind(d) + " " + dependencyRelationName(catalog, d, path)
	if target.SubID != 0 {
		for i, attribute := range d.attributes {
			if attribute == target.SubID {
				return "column " + d.columns[i].Name + " of " + object
			}
		}
	}
	return object
}

func dependencyRelationName(catalog CatalogRead, d *relationDescriptor, path []ObjectAddress) string {
	// Internal provider stages have no SQL visibility context. SQL utilities
	// supply their actual lookup path, including an empty effective path.
	if path == nil {
		return dependencyIdentifier(d.name)
	}
	return relationDiagnosticNameAt(catalog, d, path)
}

type supportedDependent struct {
	oid          OID
	target       ObjectAddress
	view         ObjectAddress
	label        string
	checkOwner   OID
	checkOrdinal int
}

// Walk the existing immediate-provider edges depth-first. Genuine identities
// order mixed siblings; same-owner CHECK siblings retain declaration order.
func supportedDependentLabels(catalog CatalogRead, targets []ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ObjectAddress) []supportedDependent {
	dependents := make([]supportedDependent, 0, len(checks)+len(views)+len(defaults))
	for _, edge := range defaults {
		label := "default value for column " + dependencyIdentifier(edge.column) + " of table " + dependencyRelationName(catalog, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.relation.defaults[edge.attribute].OID, target: edge.target, label: label})
	}
	for _, edge := range checks {
		label := "constraint " + dependencyIdentifier(edge.check.Name) + " on table " + dependencyRelationName(catalog, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.check.OID, target: edge.target, label: label, checkOwner: edge.relation.address.ObjectID, checkOrdinal: edge.check.ordinal})
	}
	for _, edge := range views {
		dependents = append(dependents, supportedDependent{oid: edge.view.address.ObjectID, target: edge.target, view: edge.view.address, label: "view " + dependencyRelationName(catalog, edge.view, path)})
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
	var visit func(ObjectAddress)
	visit = func(target ObjectAddress) {
		for i, dependent := range dependents {
			if seen[i] || dependent.target.ClassID != target.ClassID || dependent.target.ObjectID != target.ObjectID || target.SubID != 0 && dependent.target.SubID != target.SubID {
				continue
			}
			seen[i] = true
			ordered = append(ordered, dependent)
			if dependent.view != (ObjectAddress{}) {
				visit(dependent.view)
			}
		}
	}
	for _, target := range targets {
		visit(target)
	}
	return ordered
}

func supportedRestrictError(catalog CatalogRead, targets []ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ObjectAddress) error {
	dependents := supportedDependentLabels(catalog, targets, checks, views, defaults, path)
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		details[i] = dependent.label + " depends on " + dependencyObject(catalog, dependent.target, path)
	}
	// Closure edges are parent-first. Walk backwards to keep the original
	// requested provider in Message while Detail retains each actual edge.
	target := dependents[0].target
	for i := len(views) - 1; i >= 0; i-- {
		if views[i].view.address == target {
			target = views[i].target
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + dependencyObject(catalog, target, path) + " because other objects depend on it", Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func stageViewDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	stage.owner.mu.Lock()
	defer stage.owner.mu.Unlock()
	return stageViewDependentsLocked(stage, targets, mode)
}

// stageViewDependentsLocked requires the owning engine mutex throughout the
// coherent closure and removals; no provider callbacks or evaluation occur here.
func stageViewDependentsLocked(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode) error {
	edges := viewDependencyClosure(stage.base, targets, stage)
	expanded := targets
	if len(edges) != 0 {
		expanded = make([]ObjectAddress, len(targets), len(targets)+len(edges))
		copy(expanded, targets)
		for _, edge := range edges {
			expanded = append(expanded, edge.view.address)
		}
	}
	checks, err := stageCheckDependents(stage, expanded, mode)
	if err != nil {
		return err
	}
	defaults, err := stageDefaultDependents(stage, expanded, mode)
	if err != nil {
		return err
	}
	if len(edges) == 0 && len(checks) == 0 && len(defaults) == 0 {
		return nil
	}
	if mode == parser.DropRestrict {
		return supportedRestrictError(stage.base, targets, checks, edges, defaults, stage.dependencyPath)
	}
	activeEdges := edges[:0]
	for _, edge := range edges {
		d := edge.view
		if _, exists := stage.removals[d.address]; exists {
			continue
		}
		if err := stage.removeObjectLocked(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: d.address}, false); err != nil {
			return err
		}
		activeEdges = append(activeEdges, edge)
	}
	for _, dependent := range supportedDependentLabels(stage.base, targets, checks, activeEdges, defaults, stage.dependencyPath) {
		stage.viewCascadeNames = append(stage.viewCascadeNames, "drop cascades to "+dependent.label)
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
	for _, usage := range definition.castUsages {
		if usage.Type == nil {
			return fmt.Errorf("stuntgres: missing view cast type")
		}
		actual, ok := v.LookupType(usage.Type.ID())
		if !ok || actual != usage.Type || !usage.Valid() {
			return fmt.Errorf("stuntgres: invalid view cast authority")
		}
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
		if !ok || leaf.ref.owner != v.owner.owner || leaf.ref.Epoch != v.Epoch() || leaf.ref.Address.SubID != 0 {
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
	seen := make([]*sourceNode, len(definition.sources))
	projects := make(map[uint32]*projectNode, len(definition.frames))
	marked := make(map[uint32]bool)
	checkUsage := func(usage sqltype.Usage) error {
		if !usage.Valid() {
			return fmt.Errorf("stuntgres: invalid view query usage")
		}
		typ, ok := catalog.LookupType(usage.Type.ID())
		if !ok || typ != usage.Type {
			return fmt.Errorf("stuntgres: foreign view query type")
		}
		return nil
	}
	var visit func(LogicalNode, uint32) error
	visit = func(node LogicalNode, frame uint32) error {
		if node == nil {
			return fmt.Errorf("stuntgres: missing stored query node")
		}
		for _, column := range node.Schema() {
			if err := checkUsage(column.Usage); err != nil {
				return err
			}
		}
		if project, ok := node.(*projectNode); ok && project.syntax != nil {
			frame = project.frame
			frozen := definition.frames[frame]
			if frozen == nil || frozen.syntax != project.syntax {
				return fmt.Errorf("stuntgres: invalid stored query frame")
			}
			if prior := projects[frame]; prior != nil {
				if len(prior.Schema()) != len(project.Schema()) {
					return fmt.Errorf("stuntgres: inconsistent shared query width")
				}
				for i, column := range prior.Schema() {
					if column.Usage != project.Schema()[i].Usage {
						return fmt.Errorf("stuntgres: inconsistent shared query usage")
					}
				}
				return nil
			}
			projects[frame] = project
		}
		switch n := node.(type) {
		case *valuesNode:
			for _, row := range n.rows {
				if len(row) != len(n.Schema()) {
					return fmt.Errorf("stuntgres: invalid view VALUES width")
				}
				for i, expr := range row {
					if expr == nil || expr.ResultUsage().Type != n.Schema()[i].Usage.Type || expressionUsesFrame(expr, frame) {
						return fmt.Errorf("stuntgres: invalid view VALUES cell")
					}
				}
			}
		case *setNode:
			width := len(n.Schema())
			if n.left == nil || n.right == nil || len(n.left.Schema()) != width || len(n.right.Schema()) != width || len(n.leftExpressions) != width || len(n.rightExpressions) != width {
				return fmt.Errorf("stuntgres: invalid stored set width")
			}
			if n.operation != "union" && n.operation != "intersect" && n.operation != "except" {
				return fmt.Errorf("stuntgres: invalid stored set operation")
			}
			grouped := !(n.operation == "union" && n.all)
			if grouped && len(n.grouping) != width || !grouped && len(n.grouping) != 0 {
				return fmt.Errorf("stuntgres: invalid stored set grouping")
			}
			for i, column := range n.Schema() {
				if n.leftExpressions[i] == nil || n.rightExpressions[i] == nil || n.leftExpressions[i].ResultUsage() != column.Usage || n.rightExpressions[i].ResultUsage() != column.Usage {
					return fmt.Errorf("stuntgres: invalid stored set coercion")
				}
			}
		case *limitNode:
			for _, expr := range []BoundExpr{n.offset, n.count} {
				if expr != nil && (expr.ResultUsage().Type.ID() != sqltype.Int8 || expressionUsesFrame(expr, frame)) {
					return fmt.Errorf("stuntgres: invalid stored view bound")
				}
			}
			if n.withTies {
				if n.count == nil || len(n.peerKeys) == 0 {
					return fmt.Errorf("stuntgres: invalid stored WITH TIES keys")
				}
				for _, key := range n.peerKeys {
					if key.expression == nil || key.cache < -1 || key.cache < 0 && (key.slot < 0 || key.slot >= len(n.Schema())) {
						return fmt.Errorf("stuntgres: invalid stored WITH TIES slot")
					}
				}
			}
		case *sourceNode:
			if n.occurrence < 0 || n.occurrence >= len(seen) {
				return fmt.Errorf("stuntgres: invalid view source occurrence")
			}
			if prior := seen[n.occurrence]; prior != nil && prior != n {
				return fmt.Errorf("stuntgres: duplicate view source occurrence")
			}
			seen[n.occurrence] = n
			leaf := definition.sources[n.occurrence]
			source, ok := catalog.state.descriptors[leaf.ref.Address].(*relationDescriptor)
			if !ok || n.ref != leaf.ref || len(n.Schema()) != len(leaf.attributes) {
				return fmt.Errorf("stuntgres: invalid view source layout")
			}
			for _, attribute := range leaf.reads {
				for slot, frozen := range leaf.attributes {
					if frozen != attribute {
						continue
					}
					for current, live := range source.attributes {
						if live == attribute && n.Schema()[slot].Usage != source.columns[current].Usage {
							return fmt.Errorf("stuntgres: invalid view source usage")
						}
					}
				}
			}
		case *joinNode:
			if n.kind > parser.JoinFull || n.leftWidth != len(n.left.Schema()) || n.rightWidth != len(n.right.Schema()) || len(n.Schema()) != n.leftWidth+n.rightWidth+len(n.merges) {
				return fmt.Errorf("stuntgres: invalid view join frame")
			}
			for i, merge := range n.merges {
				if merge == nil || merge.ResultUsage() != n.Schema()[n.leftWidth+n.rightWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid view merged column usage")
				}
			}
		case *rowLockNode:
			project := definition.frames[frame]
			if marked[frame] || project == nil || len(project.syntax.Locking) == 0 || queryRowClause(n.firstMode) != project.syntax.Locking[0].Mode || len(n.marks) > len(definition.sources) {
				return fmt.Errorf("stuntgres: invalid stored view rowmark")
			}
			for _, mark := range n.marks {
				if mark.mode != 0 && queryRowClause(mark.mode) == "" || mark.wait > parser.RowLockNowait {
					return fmt.Errorf("stuntgres: invalid stored view rowmark")
				}
			}
			marked[frame] = true
		case *derivedNode:
			if n.query == nil || n.query.Root == nil || len(n.Schema()) != len(n.query.Root.Schema()) {
				return fmt.Errorf("stuntgres: invalid stored derived frame")
			}
		case *projectNode, *evaluationNode, *filterNode, *sortNode, *aggregateNode, *windowNode, *distinctNode, *singletonNode:
		default:
			return fmt.Errorf("stuntgres: invalid view query node %T", node)
		}
		var expressionError error
		queryExpressions(node, func(expr BoundExpr) {
			if expressionError != nil {
				return
			}
			walkBoundExpression(expr, func(expr BoundExpr) {
				if expressionError != nil {
					return
				}
				if expressionError = checkUsage(expr.ResultUsage()); expressionError != nil {
					return
				}
				if child, ok := expr.(subqueryExpression); ok {
					if child.queryPlan == nil || child.queryPlan.Root == nil {
						expressionError = fmt.Errorf("stuntgres: invalid stored subquery")
						return
					}
					if child.kind != parser.SubqueryScalar && child.kind != parser.SubqueryExists && len(child.comparisons) != len(child.queryPlan.Root.Schema()) {
						expressionError = fmt.Errorf("stuntgres: invalid stored comparison width")
						return
					}
					expressionError = visit(child.queryPlan.Root, frame)
				}
			})
		})
		if expressionError != nil {
			return expressionError
		}
		left, right := queryInputs(node)
		if left != nil {
			if err := visit(left, frame); err != nil {
				return err
			}
		}
		if right != nil {
			if err := visit(right, frame); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(definition.query.Root, 0); err != nil {
		return err
	}
	for _, source := range seen {
		if source == nil {
			return fmt.Errorf("stuntgres: invalid view source manifest")
		}
	}
	if len(projects) != len(definition.frames) {
		return fmt.Errorf("stuntgres: invalid view query frame manifest")
	}
	for frame, project := range projects {
		if project.syntax.Locking != nil && !marked[frame] {
			return fmt.Errorf("stuntgres: missing stored view rowmark")
		}
	}
	return nil
}
