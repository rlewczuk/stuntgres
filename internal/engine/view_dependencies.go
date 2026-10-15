package engine

import (
	"cmp"
	"context"
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
	provider := &dropProvider{base: catalog, stage: stage}
	return provider.viewClosure(targets)
}

func (provider *dropProvider) viewClosure(targets []ObjectAddress) []viewDependencyEdge {
	var views []*relationDescriptor
	for address, descriptor := range provider.descriptors() {
		if provider.removed(address) {
			continue
		}
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*relationDescriptor); ok && d.view != nil {
			views = append(views, d)
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

// Attached constraints have catalog identities but no standalone descriptors.
// Resolve them from their canonical parent metadata, never a spelling registry.
func constraintName(parent *relationDescriptor, address ObjectAddress) string {
	if parent == nil || address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return ""
	}
	for _, c := range parent.uniqueConstraints {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.exclusionConstraints {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.checkConstraints {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.notNullConstraints {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.foreignKeys {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	return ""
}

func constraintParent(catalog CatalogRead, address ObjectAddress) *relationDescriptor {
	if address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return nil
	}
	for _, descriptor := range catalog.state.descriptors {
		if parent, ok := descriptor.(*relationDescriptor); ok && constraintName(parent, address) != "" {
			return parent
		}
	}
	return nil
}

func dependencyObject(catalog CatalogRead, target ObjectAddress, path []ObjectAddress) string {
	base := target
	base.SubID = 0
	if parent := constraintParent(catalog, base); parent != nil {
		return "constraint " + dependencyIdentifier(constraintName(parent, base)) + " on table " + dependencyRelationName(catalog, parent, path)
	}
	if d, ok := catalog.state.descriptors[base].(*namespaceDescriptor); ok {
		return "schema " + dependencyIdentifier(d.name)
	}
	if d, ok := catalog.state.descriptors[base].(*databaseDescriptor); ok {
		return "database " + dependencyIdentifier(d.name)
	}
	if d, ok := catalog.state.descriptors[base].(*extensionDescriptor); ok {
		return "extension " + dependencyIdentifier(d.name)
	}
	if _, ok := catalog.state.descriptors[base].(*operatorDescriptor); ok {
		return "operator " + operatorSpelling(catalog, path, collectOperatorGroups(catalog), base.ObjectID, sqltype.Regoperator)
	}
	if d, ok := catalog.state.descriptors[base].(*procedureDescriptor); ok {
		name := dependencyIdentifier(d.name)
		visible := path == nil
		for _, namespace := range path {
			if n, ok := catalog.state.descriptors[namespace].(*namespaceDescriptor); ok && n.temporary != (SessionToken{}) {
				continue
			}
			if address, ok := catalog.state.names[NameKey{ClassID: ProcedureClassID, Namespace: namespace, Name: d.name, Signature: string(d.signature)}]; ok {
				visible = address == d.address
				break
			}
		}
		if !visible {
			name = dependencyIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + name
		}
		var arguments string
		switch d.signature {
		case unaccentTextSignature:
			arguments = "text"
		case unaccentDictionarySignature:
			arguments = "regdictionary,text"
		case unaccentInitSignature:
			arguments = "internal"
		case unaccentLexizeSignature:
			arguments = "internal,internal,internal,internal"
		case conversionSignature:
			arguments = "integer,integer,cstring,internal,integer,boolean"
		default:
			panic("stuntgres: invalid installed procedure signature")
		}
		return "function " + name + "(" + arguments + ")"
	}
	if d, ok := catalog.state.descriptors[base].(*tsDictionaryDescriptor); ok {
		name := dependencyIdentifier(d.name)
		visible := path == nil
		for _, ns := range path {
			if n, ok := catalog.state.descriptors[ns].(*namespaceDescriptor); ok && n.temporary != (SessionToken{}) {
				continue
			}
			if address, ok := catalog.state.names[NameKey{ClassID: TSDictionaryClassID, Namespace: ns, Name: d.name}]; ok {
				visible = address == d.address
				break
			}
		}
		if !visible {
			name = dependencyIdentifier(catalog.state.descriptors[d.namespace].Name()) + "." + name
		}
		return "text search dictionary " + name
	}
	if t, ok := catalog.state.descriptors[base].(*typeDescriptor); ok {
		return "type " + typeDiagnosticNameAt(catalog, t, path)
	}
	if d, ok := catalog.state.descriptors[base].(*collationDescriptor); ok {
		name := dependencyIdentifier(d.name)
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := catalog.state.descriptors[ns].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
					continue
				}
				if a, ok := catalog.state.names[NameKey{ClassID: CollationClassID, Namespace: ns, Name: d.name}]; ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = dependencyIdentifier(catalog.state.descriptors[d.Namespace()].Name()) + "." + name
			}
		}
		return "collation " + name
	}
	if d, ok := catalog.state.descriptors[base].(*conversionDescriptor); ok {
		name := dependencyIdentifier(d.name)
		if path != nil {
			visible := false
			for _, ns := range path {
				if namespace, ok := catalog.state.descriptors[ns].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
					continue
				}
				if a, ok := catalog.state.names[NameKey{ClassID: ConversionClassID, Namespace: ns, Name: d.name}]; ok {
					visible = a == d.Address()
					break
				}
			}
			if !visible {
				name = dependencyIdentifier(catalog.state.descriptors[d.Namespace()].Name()) + "." + name
			}
		}
		return "conversion " + name
	}
	d := catalog.state.descriptors[base].(*relationDescriptor)
	if d.sqlIndex != nil && d.sqlIndex.constraint != nil {
		parent := catalog.state.descriptors[d.sqlIndex.parent].(*relationDescriptor)
		return "constraint " + dependencyIdentifier(d.sqlIndex.constraint.Name) + " on table " + dependencyRelationName(catalog, parent, path)
	}
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
func supportedDependentLabels(catalog CatalogRead, targets []ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ObjectAddress, foreign ...domainDependency) []supportedDependent {
	policyTargets := targets
	if len(views) != 0 {
		policyTargets = make([]ObjectAddress, len(targets), len(targets)+len(views))
		copy(policyTargets, targets)
		for _, edge := range views {
			policyTargets = append(policyTargets, edge.view.address)
		}
	}
	dependents := policyDependentLabels(catalog, policyTargets, path)
	dependents = append(dependents, ruleDependentLabels(catalog, policyTargets, path)...)
	return supportedDependentLabelsWithPolicies(catalog, targets, checks, views, defaults, path, dependents, foreign...)
}

// Other labels are captured before CASCADE mutates policy and rule metadata.
func supportedDependentLabelsWithPolicies(catalog CatalogRead, targets []ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ObjectAddress, other []supportedDependent, foreign ...domainDependency) []supportedDependent {
	dependents := make([]supportedDependent, 0, len(checks)+len(views)+len(defaults)+len(other)+len(foreign))
	dependents = append(dependents, other...)
	for _, edge := range defaults {
		label := "default value for column " + dependencyIdentifier(edge.column) + " of table " + dependencyRelationName(catalog, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.relation.defaults[edge.attribute].OID, target: edge.target, label: label})
	}
	for _, edge := range checks {
		label := "constraint " + dependencyIdentifier(edge.check.Name) + " on table " + dependencyRelationName(catalog, edge.relation, path)
		dependents = append(dependents, supportedDependent{oid: edge.check.OID, target: edge.target, label: label, checkOwner: edge.relation.address.ObjectID, checkOrdinal: edge.check.ordinal})
	}
	for _, edge := range views {
		dependents = append(dependents, supportedDependent{oid: edge.view.address.ObjectID, target: edge.target, view: edge.view.address, label: relationDiagnosticKind(edge.view) + " " + dependencyRelationName(catalog, edge.view, path)})
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

func supportedRestrictError(catalog CatalogRead, targets []ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ObjectAddress, foreign ...domainDependency) error {
	dependents := supportedDependentLabels(catalog, targets, checks, views, defaults, path, foreign...)
	return supportedRestrictErrorLabels(catalog, views, path, dependents, false)
}

func supportedRestrictErrorLabels(catalog CatalogRead, views []viewDependencyEdge, path []ObjectAddress, dependents []supportedDependent, multipleDeletion bool) error {
	details := make([]string, len(dependents))
	for i, dependent := range dependents {
		provider := dependencyObject(catalog, dependent.target, path)
		// The dependency targets the physical supporting index. Its owning
		// constraint identifies the requested DROP in Message, not this edge.
		if index, ok := catalog.state.descriptors[dependent.target].(*relationDescriptor); ok && index.sqlIndex != nil {
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
			if views[i].view.address == target {
				target = views[i].target
			}
		}
		message = "cannot drop " + dependencyObject(catalog, target, path) + " because other objects depend on it"
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func stageViewDependents(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	stage.owner.mu.Lock()
	defer stage.owner.mu.Unlock()
	return stageViewDependentsLocked(stage, targets, mode, foreign...)
}

// stageViewDependentsLocked requires the owning engine mutex throughout the
// coherent closure and removals; no provider callbacks or evaluation occur here.
func stageViewDependentsLocked(stage *CatalogStage, targets []ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).viewDependents(targets, mode, foreign...)
}

func (provider *dropProvider) viewDependents(targets []ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	edges := provider.viewClosure(targets)
	expanded := targets
	if len(edges) != 0 {
		expanded = make([]ObjectAddress, len(targets), len(targets)+len(edges))
		copy(expanded, targets)
		for _, edge := range edges {
			expanded = append(expanded, edge.view.address)
		}
	}
	policies := provider.policyDependentLabels(expanded)
	policies = append(policies, provider.ruleDependentLabels(expanded)...)
	checks, err := provider.checkDependents(expanded, mode)
	if err != nil {
		return err
	}
	defaults, err := provider.defaultDependents(expanded, mode)
	if err != nil {
		return err
	}
	visibleForeign := slices.ContainsFunc(foreign, func(edge domainDependency) bool { return !edge.automatic })
	if len(edges) == 0 && len(checks) == 0 && len(defaults) == 0 && len(policies) == 0 && !visibleForeign {
		return provider.foreignKeyDependents(foreign)
	}
	if mode == parser.DropRestrict {
		labels := supportedDependentLabelsWithPolicies(provider.base, targets, checks, edges, defaults, provider.path, policies, foreign...)
		multipleDeletion := provider.scope != nil && provider.scope.inheritanceAlter != nil
		return supportedRestrictErrorLabels(provider.base, edges, provider.path, labels, multipleDeletion)
	}
	if err := provider.foreignKeyDependents(foreign); err != nil {
		return err
	}
	if err := provider.policyDependents(expanded, mode); err != nil {
		return err
	}
	if err := provider.ruleDependents(expanded, mode); err != nil {
		return err
	}
	activeEdges := edges[:0]
	for _, edge := range edges {
		d := edge.view
		if provider.removed(d.address) {
			continue
		}
		if err := provider.removeObjectLocked(ObjectRef{owner: provider.owner.owner, Epoch: provider.base.Epoch(), Address: d.address}, false); err != nil {
			return err
		}
		activeEdges = append(activeEdges, edge)
	}
	for _, dependent := range supportedDependentLabelsWithPolicies(provider.base, targets, checks, activeEdges, defaults, provider.path, policies, foreign...) {
		provider.cascadeNotice("drop cascades to " + dependent.label)
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
		if !validExpressionUsage(v, usage) {
			return fmt.Errorf("stuntgres: invalid view cast authority")
		}
		if err := validateUsageCollation(v, usage); err != nil {
			return err
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
		if !ok || !storedViewReferenceValid(v, d, leaf.ref) || leaf.ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid view source identity")
		}
		namespace, ok := v.state.descriptors[source.namespace].(*namespaceDescriptor)
		consumerNamespace, consumerOK := v.state.descriptors[d.namespace].(*namespaceDescriptor)
		if !ok || namespace.temporary != (SessionToken{}) && (!consumerOK || consumerNamespace.temporary != namespace.temporary) {
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
		if !storedViewReferenceValid(v, d, ref) {
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
		if err := validateUsageCollation(catalog, usage); err != nil {
			return err
		}
		if !validExpressionUsage(catalog, usage) {
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
		if project, ok := node.(*projectNode); ok && (project.syntax != nil || definition.frames[project.frame] == project) {
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
			if err := validateStoredSetNode(n); err != nil {
				return err
			}
		case *recursiveUnionNode:
			if n.definition == nil || !n.definition.recursive {
				return fmt.Errorf("stuntgres: invalid stored recursive definition")
			}
			if err := validateStoredSetNode(&n.setNode); err != nil {
				return err
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
		case *functionFromNode:
			if err := validateFunctionFromLayout(n); err != nil {
				return err
			}
		case *setProjectNode:
			if n.child == nil || n.template == nil || n.template.group == nil || *n.template.group != n.frame ||
				len(n.calls) == 0 || n.baseWidth < 0 || n.markerWidth != len(n.template.origins) ||
				len(n.Schema()) != n.baseWidth+n.markerWidth+len(n.template.scalars) ||
				(len(n.child.Schema()) != n.baseWidth && len(n.child.Schema()) != len(n.Schema())) ||
				len(n.callSlots) != len(n.calls) || len(n.template.scalarRequests) != len(n.template.scalars) ||
				n.scalarSlots != nil && len(n.scalarSlots) != len(n.scalars) {
				return fmt.Errorf("stuntgres: invalid stored string set frame")
			}
			if err := validateQuerySetSteps(n.template.steps, len(n.template.origins), len(n.template.scalars)); err != nil {
				return err
			}
			if err := validateQuerySetSteps(n.steps, len(n.calls), len(n.scalars)); err != nil {
				return err
			}
			if err := validateQuerySetStageOrder(n.template.steps, n.steps, n.callSlots, n.scalarSlots, len(n.scalars)); err != nil {
				return err
			}
			base := n.baseWidth
			for i, original := range n.template.origins {
				if !isQuerySet(original) || original.ResultUsage() != n.Schema()[base+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string marker layout")
				}
			}
			for i, scalar := range n.template.scalars {
				request := n.template.scalarRequests[i]
				if request < 0 || request >= len(n.template.expressions) || scalar == nil || isQuerySet(scalar) || n.template.expressions[request] == nil || isQuerySet(n.template.expressions[request]) || scalar.SourcePosition() != n.template.expressions[request].SourcePosition() || scalar.ResultUsage() != n.template.expressions[request].ResultUsage() {
					return fmt.Errorf("stuntgres: invalid stored string scalar owner")
				}
				if scalar == nil || scalar.ResultUsage() != n.Schema()[base+n.markerWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string scalar layout")
				}
				for _, previous := range n.template.scalarRequests[:i] {
					if previous == request {
						return fmt.Errorf("stuntgres: duplicate stored string scalar owner")
					}
				}
			}
			for i, call := range n.calls {
				slot := n.callSlots[i]
				if slot < 0 || slot >= n.markerWidth {
					return fmt.Errorf("stuntgres: invalid stored string call slot")
				}
				for _, previous := range n.callSlots[:i] {
					if previous == slot {
						return fmt.Errorf("stuntgres: duplicate stored string call slot")
					}
				}
				if !isQuerySet(call) || call.ResultUsage() != n.Schema()[base+slot].Usage {
					return fmt.Errorf("stuntgres: invalid stored string set usage")
				}
			}
			for i, scalar := range n.scalars {
				slot := querySetScalarSlot(n.scalarSlots, i)
				if slot < 0 || slot >= len(n.template.scalars) || scalar == nil || querySetDepth(scalar) != 0 || scalar.ResultUsage() != n.Schema()[base+n.markerWidth+slot].Usage || scalar.SourcePosition() != n.template.scalars[slot].SourcePosition() {
					return fmt.Errorf("stuntgres: invalid stored string scalar usage")
				}
				for previous := range i {
					if querySetScalarSlot(n.scalarSlots, previous) == slot {
						return fmt.Errorf("stuntgres: duplicate stored string scalar slot")
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
		case *worktableScanNode:
			if n.query != nil || n.cte == nil || !n.cte.recursive || len(n.Schema()) != len(n.cte.outputSchema()) {
				return fmt.Errorf("stuntgres: invalid stored worktable identity")
			}
		case *projectNode, *evaluationNode, *filterNode, *sortNode, *aggregateNode, *windowNode, *distinctNode, *distinctOnNode, *singletonNode:
		default:
			return fmt.Errorf("stuntgres: invalid view query node %T", node)
		}
		var expressionError error
		validateExpression := func(expr BoundExpr, contextual bool) {
			if expressionError != nil {
				return
			}
			walkBoundExpression(expr, func(expr BoundExpr) {
				if expressionError != nil {
					return
				}
				if unresolved, ok := expr.(unknownOutput); ok {
					if !contextual {
						expressionError = fmt.Errorf("stuntgres: invalid view query usage")
						return
					}
					// Set operands retain their pre-common-type literal analysis,
					// not an executable value with an invented usage. Only these
					// explicit contextual occurrences may lack a descriptor.
					literal, ok := unresolved.raw.(parser.LiteralExpr)
					if !ok || unresolved.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
						expressionError = fmt.Errorf("stuntgres: invalid contextual view output")
					}
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
		}
		if project, ok := node.(*projectNode); ok {
			for _, expr := range project.expressions {
				validateExpression(expr, false)
			}
			for _, expr := range project.analysisExpressions {
				validateExpression(expr, true)
			}
		} else if stage, ok := node.(*setProjectNode); ok {
			for _, call := range stage.calls {
				validateExpression(call, false)
			}
			for _, scalar := range stage.scalars {
				validateExpression(scalar, false)
			}
			if stage.template != nil {
				for _, root := range stage.template.expressions {
					_, contextual := root.(unknownOutput)
					validateExpression(root, contextual)
				}
				for _, origin := range stage.template.origins {
					validateExpression(origin, false)
				}
				for _, scalar := range stage.template.scalars {
					validateExpression(scalar, false)
				}
			}
		} else {
			queryExpressions(node, func(expr BoundExpr) { validateExpression(expr, false) })
		}
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
		if project.syntax != nil && project.syntax.Locking != nil && !marked[frame] {
			return fmt.Errorf("stuntgres: missing stored view rowmark")
		}
	}
	return nil
}

func validateStoredSetNode(n *setNode) error {
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
	return nil
}
