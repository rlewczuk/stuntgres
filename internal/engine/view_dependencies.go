package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func viewDepends(d *catalog.Relation, target ids.ObjectAddress) bool {
	return d.View != nil && d.View.DependsOn(target)
}

type viewDependencyEdge struct {
	view   *catalog.Relation
	target ids.ObjectAddress
}

func viewDependencyClosure(catalog catalog.Read, targets []ids.ObjectAddress, stage *CatalogStage) []viewDependencyEdge {
	provider := &dropProvider{base: catalog, stage: stage}
	return provider.viewClosure(targets)
}

func (provider *dropProvider) viewClosure(targets []ids.ObjectAddress) []viewDependencyEdge {
	var views []*catalog.Relation
	for address, descriptor := range provider.descriptors() {
		if provider.removed(address) {
			continue
		}
		if replacement := provider.replacement(address); replacement != nil {
			descriptor = replacement
		}
		if d, ok := descriptor.(*catalog.Relation); ok && d.View != nil {
			views = append(views, d)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	seen := make(map[ids.ObjectAddress]bool, len(targets))
	for _, target := range targets {
		if target.SubID == 0 {
			seen[target] = true
		}
	}
	var edges []viewDependencyEdge
	var visit func(ids.ObjectAddress)
	visit = func(target ids.ObjectAddress) {
		for _, view := range views {
			if !seen[view.Address()] && viewDepends(view, target) {
				seen[view.Address()] = true
				edges = append(edges, viewDependencyEdge{view, target})
				visit(view.Address())
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
func constraintName(parent *catalog.Relation, address ids.ObjectAddress) string {
	if parent == nil || address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return ""
	}
	for _, c := range parent.Constraints.Unique {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.Exclusion {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.Check {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.NotNull {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.ForeignKeys {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	return ""
}

func constraintParent(view catalog.Read, address ids.ObjectAddress) *catalog.Relation {
	if address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		if parent, ok := descriptor.(*catalog.Relation); ok && constraintName(parent, address) != "" {
			return parent
		}
	}
	return nil
}

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
		return foreignLabel(d.Address().ClassID) + " " + catalog.DependencyIdentifier(d.Name())
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
	return relationDiagnosticNameAt(catalogView, d, path)
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

func supportedRestrictError(catalog catalog.Read, targets []ids.ObjectAddress, checks []checkDependencyEdge, views []viewDependencyEdge, defaults []defaultDependencyEdge, path []ids.ObjectAddress, foreign ...domainDependency) error {
	dependents := supportedDependentLabels(catalog, targets, checks, views, defaults, path, foreign...)
	return supportedRestrictErrorLabels(catalog, views, path, dependents, false)
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

func stageViewDependents(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	stage.owner.mu.Lock()
	defer stage.owner.mu.Unlock()
	return stageViewDependentsLocked(stage, targets, mode, foreign...)
}

// stageViewDependentsLocked requires the owning engine mutex throughout the
// coherent closure and removals; no provider callbacks or evaluation occur here.
func stageViewDependentsLocked(stage *CatalogStage, targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).viewDependents(targets, mode, foreign...)
}

func (provider *dropProvider) viewDependents(targets []ids.ObjectAddress, mode parser.DropMode, foreign ...domainDependency) error {
	edges := provider.viewClosure(targets)
	expanded := targets
	if len(edges) != 0 {
		expanded = make([]ids.ObjectAddress, len(targets), len(targets)+len(edges))
		copy(expanded, targets)
		for _, edge := range edges {
			expanded = append(expanded, edge.view.Address())
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
		multipleDeletion := provider.scope != nil && provider.scope.inheritance.alter != nil
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
		if provider.removed(d.Address()) {
			continue
		}
		if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), d.Address()), false); err != nil {
			return err
		}
		activeEdges = append(activeEdges, edge)
	}
	for _, dependent := range supportedDependentLabelsWithPolicies(provider.base, targets, checks, activeEdges, defaults, provider.path, policies, foreign...) {
		provider.cascadeNotice("drop cascades to " + dependent.label)
	}
	return nil
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

func validateViewDefinition(v catalog.Read, d *catalog.Relation) error {
	if d.View == nil {
		return fmt.Errorf("stuntgres: invalid view definition")
	}
	return d.View.Validate(v, d)
}

// Validate implements viewPayload: it checks the frozen definition against
// its owning relation d in catalog v.
func (definition *viewDefinition) Validate(v catalog.Read, d *catalog.Relation) error {
	if definition == nil || definition.syntax == nil || definition.query == nil || definition.query.Root == nil || definition.rule == 0 || len(definition.query.Root.Schema()) != len(d.Columns) {
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
		source, ok := v.Relation(leaf.ref.Address)
		if !ok || !storedViewReferenceValid(v, d, leaf.ref) || leaf.ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid view source identity")
		}
		namespace, ok := v.Namespace(source.Namespace())
		consumerNamespace, consumerOK := v.Namespace(d.Namespace())
		if !ok || namespace.Temporary != (ids.SessionToken{}) && (!consumerOK || consumerNamespace.Temporary != namespace.Temporary) {
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
			if !source.HasAttribute(attribute) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	for _, dependency := range definition.dependencies {
		ref := dependency.Ref
		address := ref.Address
		address.SubID = 0
		if !storedViewReferenceValid(v, d, ref) {
			return fmt.Errorf("stuntgres: invalid view dependency identity")
		}
		object, ok := v.Find(address)
		if !ok {
			return fmt.Errorf("stuntgres: missing view dependency")
		}
		if ref.Address.SubID != 0 {
			relation, ok := object.(*catalog.Relation)
			if !ok || !relation.HasAttribute(ref.Address.SubID) {
				return fmt.Errorf("stuntgres: missing view dependency attribute")
			}
		}
	}
	return nil
}

func validateViewLeaves(catalog catalog.Read, definition *viewDefinition) error {
	seen := make([]*plan.SourceNode, len(definition.sources))
	projects := make(map[uint32]*plan.ProjectNode, len(definition.frames))
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
	var visit func(plan.Node, uint32) error
	visit = func(node plan.Node, frame uint32) error {
		if node == nil {
			return fmt.Errorf("stuntgres: missing stored query node")
		}
		for _, column := range node.Schema() {
			if err := checkUsage(column.Usage); err != nil {
				return err
			}
		}
		if project, ok := node.(*plan.ProjectNode); ok && (project.Syntax != nil || definition.frames[project.Frame] == project) {
			frame = project.Frame
			frozen := definition.frames[frame]
			if frozen == nil || frozen.Syntax != project.Syntax {
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
		case *plan.ValuesNode:
			for _, row := range n.Rows {
				if len(row) != len(n.Schema()) {
					return fmt.Errorf("stuntgres: invalid view VALUES width")
				}
				for i, expr := range row {
					if expr == nil || expr.ResultUsage().Type != n.Schema()[i].Usage.Type || analyze.ExpressionUsesFrame(expr, frame) {
						return fmt.Errorf("stuntgres: invalid view VALUES cell")
					}
				}
			}
		case *plan.SetNode:
			if err := validateStoredSetNode(n, nil, nil); err != nil {
				return err
			}
		case *plan.RecursiveUnionNode:
			if n.Definition == nil || !n.Definition.Recursive {
				return fmt.Errorf("stuntgres: invalid stored recursive definition")
			}
			if err := validateStoredSetNode(&n.SetNode, n.Cycle, n.Search); err != nil {
				return err
			}
			if n.Search != nil || n.Cycle != nil {
				if n.Definition.UserWidth != len(n.LeftExpressions) {
					return fmt.Errorf("stuntgres: invalid stored original width")
				}
				scan, offset := analyze.RecursiveParentSlot(n.Right.Child, n.Definition, 0)
				if scan == nil {
					return fmt.Errorf("stuntgres: missing stored recursive parent")
				}
				for slot := n.Definition.UserWidth; slot < len(n.Schema()); slot++ {
					carry, ok := n.RightExpressions[slot].(plan.ColumnExpr)
					if !ok || carry.Slot != offset+slot || carry.Frame != n.Right.Frame || slot >= len(scan.Demand) || !scan.Demand[slot] {
						return fmt.Errorf("stuntgres: invalid stored recursive parent carry")
					}
				}
			}
		case *plan.LimitNode:
			for _, expr := range []expr.Expr{n.Offset, n.Count} {
				if expr != nil && (expr.ResultUsage().Type.ID() != sqltype.Int8 || analyze.ExpressionUsesFrame(expr, frame)) {
					return fmt.Errorf("stuntgres: invalid stored view bound")
				}
			}
			if n.WithTies {
				if n.Count == nil || len(n.PeerKeys) == 0 {
					return fmt.Errorf("stuntgres: invalid stored WITH TIES keys")
				}
				for _, key := range n.PeerKeys {
					if key.Expression == nil || key.Cache < -1 || key.Cache < 0 && (key.Slot < 0 || key.Slot >= len(n.Schema())) {
						return fmt.Errorf("stuntgres: invalid stored WITH TIES slot")
					}
				}
			}
		case *plan.FunctionFromNode:
			if err := exec.ValidateFunctionFromLayout(n); err != nil {
				return err
			}
		case *plan.SetProjectNode:
			if n.Child == nil || n.Template == nil || n.Template.Group == nil || *n.Template.Group != n.Frame ||
				len(n.Calls) == 0 || n.BaseWidth < 0 || n.MarkerWidth != len(n.Template.Origins) ||
				len(n.Schema()) != n.BaseWidth+n.MarkerWidth+len(n.Template.Scalars) ||
				(len(n.Child.Schema()) != n.BaseWidth && len(n.Child.Schema()) != len(n.Schema())) ||
				len(n.CallSlots) != len(n.Calls) || len(n.Template.ScalarRequests) != len(n.Template.Scalars) ||
				n.ScalarSlots != nil && len(n.ScalarSlots) != len(n.Scalars) {
				return fmt.Errorf("stuntgres: invalid stored string set frame")
			}
			if err := exec.ValidateQuerySetSteps(n.Template.Steps, len(n.Template.Origins), len(n.Template.Scalars)); err != nil {
				return err
			}
			if err := exec.ValidateQuerySetSteps(n.Steps, len(n.Calls), len(n.Scalars)); err != nil {
				return err
			}
			if err := exec.ValidateQuerySetStageOrder(n.Template.Steps, n.Steps, n.CallSlots, n.ScalarSlots, len(n.Scalars)); err != nil {
				return err
			}
			base := n.BaseWidth
			for i, original := range n.Template.Origins {
				if !exec.SourceReturnsSet(original) || original.ResultUsage() != n.Schema()[base+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string marker layout")
				}
			}
			for i, scalar := range n.Template.Scalars {
				request := n.Template.ScalarRequests[i]
				if request < 0 || request >= len(n.Template.Expressions) || scalar == nil || exec.SourceReturnsSet(scalar) || n.Template.Expressions[request] == nil || exec.SourceReturnsSet(n.Template.Expressions[request]) || scalar.SourcePosition() != n.Template.Expressions[request].SourcePosition() || scalar.ResultUsage() != n.Template.Expressions[request].ResultUsage() {
					return fmt.Errorf("stuntgres: invalid stored string scalar owner")
				}
				if scalar == nil || scalar.ResultUsage() != n.Schema()[base+n.MarkerWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid stored string scalar layout")
				}
				for _, previous := range n.Template.ScalarRequests[:i] {
					if previous == request {
						return fmt.Errorf("stuntgres: duplicate stored string scalar owner")
					}
				}
			}
			for i, call := range n.Calls {
				slot := n.CallSlots[i]
				if slot < 0 || slot >= n.MarkerWidth {
					return fmt.Errorf("stuntgres: invalid stored string call slot")
				}
				for _, previous := range n.CallSlots[:i] {
					if previous == slot {
						return fmt.Errorf("stuntgres: duplicate stored string call slot")
					}
				}
				if !exec.SourceReturnsSet(call) || call.ResultUsage() != n.Schema()[base+slot].Usage {
					return fmt.Errorf("stuntgres: invalid stored string set usage")
				}
			}
			for i, scalar := range n.Scalars {
				slot := exec.QuerySetScalarSlot(n.ScalarSlots, i)
				if slot < 0 || slot >= len(n.Template.Scalars) || scalar == nil || exec.QuerySetDepth(scalar) != 0 || scalar.ResultUsage() != n.Schema()[base+n.MarkerWidth+slot].Usage || scalar.SourcePosition() != n.Template.Scalars[slot].SourcePosition() {
					return fmt.Errorf("stuntgres: invalid stored string scalar usage")
				}
				for previous := range i {
					if exec.QuerySetScalarSlot(n.ScalarSlots, previous) == slot {
						return fmt.Errorf("stuntgres: duplicate stored string scalar slot")
					}
				}
			}
		case *plan.SourceNode:
			if n.Occurrence < 0 || n.Occurrence >= len(seen) {
				return fmt.Errorf("stuntgres: invalid view source occurrence")
			}
			if prior := seen[n.Occurrence]; prior != nil && prior != n {
				return fmt.Errorf("stuntgres: duplicate view source occurrence")
			}
			seen[n.Occurrence] = n
			leaf := definition.sources[n.Occurrence]
			source, ok := catalog.Relation(leaf.ref.Address)
			if !ok || n.Ref != leaf.ref || len(n.Schema()) != len(leaf.attributes) {
				return fmt.Errorf("stuntgres: invalid view source layout")
			}
			for _, attribute := range leaf.reads {
				for slot, frozen := range leaf.attributes {
					if frozen != attribute {
						continue
					}
					for current, live := range source.Attributes {
						if live == attribute && n.Schema()[slot].Usage != source.Columns[current].Usage {
							return fmt.Errorf("stuntgres: invalid view source usage")
						}
					}
				}
			}
		case *plan.JoinNode:
			if n.Kind > parser.JoinFull || n.LeftWidth != len(n.Left.Schema()) || n.RightWidth != len(n.Right.Schema()) || len(n.Schema()) != n.LeftWidth+n.RightWidth+len(n.Merges) {
				return fmt.Errorf("stuntgres: invalid view join frame")
			}
			for i, merge := range n.Merges {
				if merge == nil || merge.ResultUsage() != n.Schema()[n.LeftWidth+n.RightWidth+i].Usage {
					return fmt.Errorf("stuntgres: invalid view merged column usage")
				}
			}
		case *plan.RowLockNode:
			project := definition.frames[frame]
			if marked[frame] || project == nil || len(project.Syntax.Locking) == 0 || analyze.RowLockClause(n.FirstMode) != project.Syntax.Locking[0].Mode || len(n.Marks) > len(definition.sources) {
				return fmt.Errorf("stuntgres: invalid stored view rowmark")
			}
			for _, mark := range n.Marks {
				if mark.Mode != 0 && analyze.RowLockClause(mark.Mode) == "" || mark.Wait > parser.RowLockNowait {
					return fmt.Errorf("stuntgres: invalid stored view rowmark")
				}
			}
			marked[frame] = true
		case *plan.DerivedNode:
			if n.Query == nil || n.Query.Root == nil || len(n.Schema()) != len(n.Query.Root.Schema()) {
				return fmt.Errorf("stuntgres: invalid stored derived frame")
			}
		case *plan.WorktableScanNode:
			if n.Query != nil || n.CTE == nil || !n.CTE.Recursive || len(n.Schema()) != len(n.CTE.OutputSchema()) {
				return fmt.Errorf("stuntgres: invalid stored worktable identity")
			}
		case *plan.ProjectNode, *plan.EvaluationNode, *plan.FilterNode, *plan.SortNode, *plan.AggregateNode, *plan.WindowNode, *plan.DistinctNode, *plan.DistinctOnNode, *plan.SingletonNode:
		default:
			return fmt.Errorf("stuntgres: invalid view query node %T", node)
		}
		var expressionError error
		validateExpression := func(bound expr.Expr, contextual bool) {
			if expressionError != nil {
				return
			}
			plan.WalkExpression(bound, func(expr expr.Expr) {
				if expressionError != nil {
					return
				}
				if unresolved, ok := expr.(plan.UnknownOutput); ok {
					if !contextual {
						expressionError = fmt.Errorf("stuntgres: invalid view query usage")
						return
					}
					// Set operands retain their pre-common-type literal analysis,
					// not an executable value with an invented usage. Only these
					// explicit contextual occurrences may lack a descriptor.
					literal, ok := unresolved.Raw.(parser.LiteralExpr)
					if !ok || unresolved.ResultUsage() != (sqltype.Usage{}) || literal.Value.Kind != parser.StringValue && literal.Value.Kind != parser.NullValue {
						expressionError = fmt.Errorf("stuntgres: invalid contextual view output")
					}
					return
				}
				if expressionError = checkUsage(expr.ResultUsage()); expressionError != nil {
					return
				}
				if child, ok := expr.(plan.SubqueryExpr); ok {
					if child.QueryPlan == nil || child.QueryPlan.Root == nil {
						expressionError = fmt.Errorf("stuntgres: invalid stored subquery")
						return
					}
					if child.Kind != parser.SubqueryScalar && child.Kind != parser.SubqueryExists && len(child.Comparisons) != len(child.QueryPlan.Root.Schema()) {
						expressionError = fmt.Errorf("stuntgres: invalid stored comparison width")
						return
					}
					expressionError = visit(child.QueryPlan.Root, frame)
				}
			})
		}
		if project, ok := node.(*plan.ProjectNode); ok {
			for _, expr := range project.Expressions {
				validateExpression(expr, false)
			}
			for _, expr := range project.AnalysisExpressions {
				validateExpression(expr, true)
			}
		} else if stage, ok := node.(*plan.SetProjectNode); ok {
			for _, call := range stage.Calls {
				validateExpression(call, false)
			}
			for _, scalar := range stage.Scalars {
				validateExpression(scalar, false)
			}
			if stage.Template != nil {
				for _, root := range stage.Template.Expressions {
					_, contextual := root.(plan.UnknownOutput)
					validateExpression(root, contextual)
				}
				for _, origin := range stage.Template.Origins {
					validateExpression(origin, false)
				}
				for _, scalar := range stage.Template.Scalars {
					validateExpression(scalar, false)
				}
			}
		} else {
			plan.QueryExpressions(node, func(expr expr.Expr) { validateExpression(expr, false) })
		}
		if expressionError != nil {
			return expressionError
		}
		left, right := plan.QueryInputs(node)
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
		if project.Syntax != nil && project.Syntax.Locking != nil && !marked[frame] {
			return fmt.Errorf("stuntgres: missing stored view rowmark")
		}
	}
	return nil
}

func validateStoredSetNode(n *plan.SetNode, cycle *plan.CycleRecipe, search *plan.SearchRecipe) error {
	width := len(n.Schema())
	leftWidth, groupingWidth := width, width
	if cycle != nil {
		if cycle.MarkSlot < 0 || width != cycle.MarkSlot+2 ||
			n.Schema()[cycle.MarkSlot].Usage != cycle.Mark.ResultUsage() ||
			n.Schema()[cycle.MarkSlot+1].Usage.Type != cycle.Path {
			return fmt.Errorf("stuntgres: invalid stored cycle layout")
		}
		leftWidth = cycle.MarkSlot
		groupingWidth--
	}
	if search != nil {
		if search.Syntax == nil || search.Result == nil {
			return fmt.Errorf("stuntgres: invalid stored search recipe")
		}
		expected := search.Slot + 1
		if cycle != nil {
			expected += 2
		}
		if search.Slot < 0 || width != expected || cycle != nil && cycle.MarkSlot != search.Slot+1 ||
			n.Schema()[search.Slot].Usage != search.Result.Usage() ||
			search.Record == nil || !search.Record.IsComposite() || search.Record.ID() != 2249 ||
			(search.Syntax.Breadth && search.Result != search.Record) ||
			(!search.Syntax.Breadth && (!sqltype.OccurrenceRecordArray(search.Result) || search.Result.Element() != search.Record)) {
			return fmt.Errorf("stuntgres: invalid stored search layout")
		}
		if !n.All && len(search.Grouping) != search.Record.CompositeLen() || n.All && len(search.Grouping) != 0 {
			return fmt.Errorf("stuntgres: invalid stored search equality")
		}
		offset := 0
		if search.Syntax.Breadth {
			offset = 1
		}
		if len(search.Keys)+offset != search.Record.CompositeLen() {
			return fmt.Errorf("stuntgres: invalid stored search keys")
		}
		for i, key := range search.Keys {
			if key < 0 || key >= search.Slot || n.Schema()[key].Usage != search.Record.CompositeMember(i+offset).Usage {
				return fmt.Errorf("stuntgres: invalid stored search key usage")
			}
		}
		leftWidth = search.Slot
		groupingWidth--
	}
	if n.Left == nil || n.Right == nil || len(n.Left.Schema()) != leftWidth || len(n.Right.Schema()) != width || len(n.LeftExpressions) != leftWidth || len(n.RightExpressions) != width {
		return fmt.Errorf("stuntgres: invalid stored set width")
	}
	if n.Operation != "union" && n.Operation != "intersect" && n.Operation != "except" {
		return fmt.Errorf("stuntgres: invalid stored set operation")
	}
	grouped := !(n.Operation == "union" && n.All)
	if grouped && len(n.Grouping) != groupingWidth || !grouped && len(n.Grouping) != 0 {
		return fmt.Errorf("stuntgres: invalid stored set grouping")
	}
	for i, column := range n.Schema() {
		if n.RightExpressions[i] == nil || n.RightExpressions[i].ResultUsage() != column.Usage ||
			i < leftWidth && (n.LeftExpressions[i] == nil || n.LeftExpressions[i].ResultUsage() != column.Usage) {
			return fmt.Errorf("stuntgres: invalid stored set coercion")
		}
	}
	return nil
}
