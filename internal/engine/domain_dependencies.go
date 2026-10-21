package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type domainDependency struct {
	address, target, root ids.ObjectAddress
	oid                   ids.OID
	ordinal               int32
	label                 string
	relation              *catalog.Relation
	domain                *catalog.Type
	column                string
	automatic             bool
}

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func domainDependencyClosure(ctx context.Context, stage *CatalogStage, targets []ids.ObjectAddress) ([]domainDependency, error) {
	return stageDropProvider(ctx, stage, nil, nil, nil).dependencyClosure(targets)
}

func (provider *dropProvider) dependencyClosure(targets []ids.ObjectAddress) ([]domainDependency, error) {
	ctx := provider.ctx
	domains := make(map[ids.ObjectAddress]bool, len(targets))
	for _, a := range targets {
		domains[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a, d := range provider.inputDescriptors() {
			if t, ok := d.(*catalog.Type); ok && !domains[a] && (t.SQLType.IsDomain() && (domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Type.ID())}] || domains[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Collation.ID)}]) || t.SQLType.IsArray() && domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.Element().ID())}]) {
				domains[a] = true
				changed = true
			}
		}
	}
	droppedColumns := make(map[ids.ObjectAddress]bool)
	for a, d := range provider.inputDescriptors() {
		r, ok := d.(*catalog.Relation)
		if !ok || r.RelationKind != catalog.TableRelation && r.RelationKind != catalog.PartitionedRelation && r.RelationKind != catalog.CompositeRelation && r.RelationKind != catalog.ForeignRelation {
			continue
		}
		for i, c := range r.Columns {
			if domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}] || domains[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(c.Usage.Collation.ID)}] {
				column := a
				column.SubID = r.Attributes[i]
				droppedColumns[column] = true
			}
		}
	}
	seen := make(map[ids.ObjectAddress]bool)
	for _, a := range targets {
		seen[a] = true
	}
	var result []domainDependency
	resultSlots := make(map[ids.ObjectAddress]int)
	// Only composite-attribute roots with real domain-default edges use the
	// released reverse-postorder walk. CHECK-only and other provider routes
	// retain their established traversal and notice contracts.
	compositeAttributeRoot := false
	if len(targets) == 1 && targets[0].ClassID == ids.RelationClassID && targets[0].SubID > 0 {
		address := targets[0]
		address.SubID = 0
		relation, ok := provider.descriptor(address).(*catalog.Relation)
		compositeAttributeRoot = ok && relation.RelationKind == catalog.CompositeRelation && relation.IndexForAttribute(targets[0].SubID) >= 0
	}
	compositeDefaultOrder := false
	var postorder []int
	mergedDescriptors := provider.descriptorCount() != provider.base.DescriptorCount()
	var visit func(ids.ObjectAddress, ids.ObjectAddress) error
	visit = func(target, root ids.ObjectAddress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relation, ok := provider.retirementDescriptor(target).(*catalog.Relation); ok && relation.RowType != 0 {
			if err := visit(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}, root); err != nil {
				return err
			}
			row, ok := provider.retirementDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}).(*catalog.Type)
			if !ok || row.OwningRelation != relation.Address() {
				return fmt.Errorf("stuntgres: missing row type dependency owner")
			}
			// Removal already stages the owned array, so the descriptor scan
			// below cannot discover it as an automatic dependent.
			if row.Array != 0 {
				if err := visit(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}, root); err != nil {
					return err
				}
			}
		}
		if relation, ok := provider.retirementDescriptor(target).(*catalog.Relation); ok && relation.Partition != nil {
			for _, child := range relation.Partition.Children {
				if provider.removed(child) {
					if err := visit(child, root); err != nil {
						return err
					}
				}
			}
		}
		var direct []domainDependency
		for a, descriptor := range provider.descriptors() {
			if mergedDescriptors {
				if replacement := provider.replacement(a); replacement != nil {
					descriptor = replacement
				}
			}
			// Owned indexes are closure roots even if an earlier pass already
			// staged their removal. Their external consumers still need pruning,
			// just like consumers of owned row types and associated arrays.
			if index, ok := descriptor.(*catalog.Relation); ok && index.SQLIndex != nil && index.SQLIndex.Parent == target {
				direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true, relation: index})
				continue
			}
			if provider.removed(a) {
				continue
			}
			if target.ClassID == ids.NamespaceClassID && descriptor.Namespace() == target && access.OwnershipRoot(provider.base, descriptor, routine.OperatorSeeded) {
				edge := domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)}
				edge.relation, _ = descriptor.(*catalog.Relation)
				direct = append(direct, edge)
				continue
			}
			switch d := descriptor.(type) {
			case *catalog.Operator:
				if routine.OperatorDependsOn(provider.base, d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
				}
			case *catalog.ForeignObject:
				for _, dependency := range pgcatalog.ForeignDependencies(d) {
					if dependency == (ids.ObjectAddress{}) {
						continue
					}
					if dependency == target {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
						break
					}
				}
			case *catalog.Extension:
				if d.TargetNamespace == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
				}
			case *catalog.Procedure:
				if d.SQL != nil && target.ClassID == ids.TypeClassID {
					uses := d.Result == target.ObjectID
					for _, input := range d.SQL.Inputs {
						uses = uses || ids.OID(input.Type.ID()) == target.ObjectID
					}
					if uses {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
					}
				}
				if d.Extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *catalog.TSDictionary:
				if d.Extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *catalog.TSTemplate:
				if d.Extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *catalog.Type:
				if replacement, ok := provider.replacement(a).(*catalog.Type); ok {
					d = replacement
				}
				if !domains[a] {
					for _, check := range d.DomainChecks {
						for _, dep := range check.Dependencies {
							if dep.Ref.Address == target {
								direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}, target: target, root: root, oid: check.OID, label: "constraint " + catalog.DependencyIdentifier(check.Name), domain: d})
								break
							}
						}
					}
				}
				defaultDependency, defaultRootDependency := false, false
				if d.DomainDefault != nil {
					for _, ref := range d.DomainDefault.Dependencies {
						defaultDependency = defaultDependency || ref.Address == target
						defaultRootDependency = defaultRootDependency || ref.Address == root
						if defaultDependency && defaultRootDependency {
							break
						}
					}
				}
				if compositeAttributeRoot && target == root && defaultDependency {
					compositeDefaultOrder = true
				}
				if d.SQLType.IsDomain() && (typedef.UsageDependsOn(d.SQLType.DomainBase(), target) || defaultDependency) {
					dependencyTarget := target
					// DFS can reach an inherited recipe through its parent before
					// visiting its direct provider edge. Retain that genuine root
					// dependency instead of reporting merely the ancestry path.
					if defaultRootDependency {
						dependencyTarget = root
					}
					direct = append(direct, domainDependency{address: a, target: dependencyTarget, root: root, oid: a.ObjectID, label: "type " + catalog.TypeDiagnosticNameAt(provider.base, d, provider.path)})
				}
				if d.SQLType.IsArray() && target == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *catalog.Relation:
				if replacement, ok := provider.replacement(a).(*catalog.Relation); ok {
					d = replacement
				}
				if d.Foreign != nil && d.Foreign.Server == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path), relation: d})
				}
				if d.RelationKind == catalog.CompositeRelation && target == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true})
					continue
				}
				if materializedExtensionDepends(d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true, label: "materialized view " + dependencyRelationName(provider.base, d, provider.path)})
				}
				if d.Membership != nil && d.Membership.Parent == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true})
				}
				if partitionDependsOn(d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, label: "table " + dependencyRelationName(provider.base, d, provider.path)})
					continue
				}
				if d.SQLIndex != nil {
					depends, automatic := false, false
					for _, key := range d.SQLIndex.Keys {
						depends = depends || target == (ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
						if key.Derived != nil {
							for _, dep := range key.Derived.Dependencies {
								depends = depends || dep.Address == target
								automatic = automatic || droppedColumns[dep.Address]
							}
						}
					}
					if predicate := d.SQLIndex.Predicate; predicate != nil {
						for _, dep := range predicate.Dependencies {
							depends = depends || dep.Address == target
							automatic = automatic || droppedColumns[dep.Address]
						}
					}
					if depends && !automatic {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "index " + dependencyRelationName(provider.base, d, provider.path), relation: d})
					}
					continue
				}
				var name string
				for _, rule := range rulesOf(d) {
					if ruleDepends(provider.base, rule, target) {
						if name == "" {
							name = dependencyRelationName(provider.base, d, provider.path)
						}
						direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: 2618, ObjectID: rule.OID}, target: target, root: root, oid: rule.OID, label: "rule " + catalog.DependencyIdentifier(rule.Name) + " on " + relationDiagnosticKind(d) + " " + name, relation: d})
					}
				}
				if d.View != nil {
					if viewDepends(d, target) {
						if compositeDefaultOrder {
							passive, err := provider.passiveViewTypeDependency(d, target)
							if err != nil {
								return err
							}
							if passive {
								continue
							}
						}
						if name == "" {
							name = dependencyRelationName(provider.base, d, provider.path)
						}
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: relationDiagnosticKind(d) + " " + name, relation: d})
					}
					continue
				}
				if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.CompositeRelation && d.RelationKind != catalog.ForeignRelation {
					continue
				}
				if name == "" {
					name = dependencyRelationName(provider.base, d, provider.path)
				}
				if d.OfType == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "table " + name, relation: d})
					continue
				}
				if d.Inheritance != nil {
					for _, parent := range d.Inheritance.Parents {
						if parent == target {
							direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "table " + name, relation: d})
							break
						}
					}
				}
				for _, policy := range d.Security.Policies {
					if policyDepends(policy, target) {
						direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}, target: target, root: root, oid: policy.OID, label: "policy " + catalog.DependencyIdentifier(policy.Name) + " on table " + name, relation: d})
					}
				}
				for i, c := range d.Columns {
					column := a
					column.SubID = d.Attributes[i]
					if typedef.UsageDependsOn(c.Usage, target) {
						direct = append(direct, domainDependency{address: column, target: target, root: root, oid: a.ObjectID, ordinal: column.SubID, label: "column " + catalog.DependencyIdentifier(c.Name) + " of " + relationDiagnosticKind(d) + " " + name, relation: d, column: c.Name})
					}
					if droppedColumns[column] {
						continue
					}
					if definition, ok := d.ColumnMeta.Defaults[column.SubID]; ok {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}, target: target, root: root, oid: definition.OID, label: "default value for column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, relation: d})
								break
							}
						}
					}
					if definition, generated := d.ColumnMeta.Generations[column.SubID]; generated && target != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}) {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								direct = append(direct, domainDependency{address: column, target: target, root: root, oid: a.ObjectID, ordinal: column.SubID, label: "column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, relation: d, column: c.Name})
								break
							}
						}
					}
				}
				for _, check := range d.Constraints.Check {
					automatic := false
					depends := false
					for _, dep := range check.Dependencies {
						automatic = automatic || droppedColumns[dep.Ref.Address]
						depends = depends || dep.Ref.Address == target
					}
					if depends && !automatic {
						orderOID, ordinal := check.OID, int32(0)
						if target.ClassID == ids.RelationClassID {
							orderOID, ordinal = a.ObjectID, int32(check.Ordinal)
						}
						direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}, target: target, root: root, oid: orderOID, ordinal: ordinal, label: "constraint " + catalog.DependencyIdentifier(check.Name) + " on table " + name, relation: d})
					}
				}
				for _, f := range d.Constraints.ForeignKeys {
					depends := target == f.Index || target == f.Target
					if target.ClassID == f.Target.ClassID && target.ObjectID == f.Target.ObjectID && target.SubID != 0 {
						depends = slices.Contains(f.Referenced, target.SubID)
					}
					automatic := target.ClassID == d.Address().ClassID && target.ObjectID == d.Address().ObjectID && (target.SubID == 0 || slices.Contains(f.Attributes, target.SubID))
					if depends || automatic {
						direct = append(direct, domainDependency{address: ids.ObjectAddress{ClassID: 2606, ObjectID: f.OID}, target: target, root: root, oid: f.OID, label: "constraint " + catalog.DependencyIdentifier(f.Name) + " on table " + name, relation: d, automatic: automatic})
					}
				}
			}
		}
		sort.Slice(direct, func(i, j int) bool {
			if compositeDefaultOrder {
				if direct[i].address.ObjectID != direct[j].address.ObjectID {
					return direct[i].address.ObjectID > direct[j].address.ObjectID
				}
				if direct[i].address.ClassID != direct[j].address.ClassID {
					return direct[i].address.ClassID < direct[j].address.ClassID
				}
				return direct[i].address.SubID < direct[j].address.SubID
			}
			if direct[i].oid != direct[j].oid {
				return direct[i].oid < direct[j].oid
			}
			// Columns and relation-target CHECKs share their table's primary
			// identity. Order those real dependent classes before their own
			// stable attribute/declaration ordinals, not CHECK enforcement OIDs.
			if direct[i].address.ClassID != direct[j].address.ClassID {
				return direct[i].address.ClassID < direct[j].address.ClassID
			}
			return direct[i].ordinal < direct[j].ordinal
		})
		for _, edge := range direct {
			if seen[edge.address] {
				if slot, exists := resultSlots[edge.address]; exists && edge.automatic && !result[slot].automatic {
					result[slot].automatic = true
					result[slot].target = edge.target
					result[slot].root = edge.root
				}
				continue
			}
			seen[edge.address] = true
			slot := len(result)
			resultSlots[edge.address] = len(result)
			result = append(result, edge)
			if edge.address.ClassID == ids.TypeClassID || edge.address.ClassID == ids.RelationClassID || edge.address.ClassID == ids.ExtensionClassID || edge.address.ClassID == ids.ProcedureClassID || edge.address.ClassID == ids.TSTemplateClassID || edge.address.ClassID == ids.TSDictionaryClassID || edge.address.ClassID == ids.ForeignWrapperClassID || edge.address.ClassID == ids.ForeignServerClassID || edge.address.ClassID == ids.ForeignMappingClassID {
				if err := visit(edge.address, root); err != nil {
					return err
				}
			}
			if compositeDefaultOrder {
				postorder = append(postorder, slot)
			}
		}
		return nil
	}
	for _, root := range targets {
		if err := visit(root, root); err != nil {
			return nil, err
		}
	}
	if compositeDefaultOrder {
		// Keep original slots throughout DFS so repeated/automatic edges use
		// the same authority. Then apply reverse postorder in place, without
		// a second dependency graph or duplicate descriptor/edge buffers.
		for position, slot := range postorder {
			resultSlots[result[slot].address] = len(result) - 1 - position
		}
		for i := range result {
			for destination := resultSlots[result[i].address]; destination != i; destination = resultSlots[result[i].address] {
				result[i], result[destination] = result[destination], result[i]
			}
		}
	}
	return result, nil
}

func domainRestrictError(stage *CatalogStage, targets []ids.ObjectAddress, edges []domainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).restrictError(targets, edges, originalRootCount, subject)
}

func (provider *dropProvider) restrictError(targets []ids.ObjectAddress, edges []domainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	message := "cannot drop desired object(s) because other objects depend on them"
	primary := subject
	if primary == (ids.ObjectAddress{}) {
		for i, target := range targets {
			var owner ids.ObjectAddress
			switch descriptor := provider.retirementDescriptor(target).(type) {
			case *catalog.Type:
				owner = descriptor.OwningRelation
			case *catalog.Relation:
				if descriptor.Sequence != nil {
					owner = descriptor.Sequence.Owned
					owner.SubID = 0
				}
			}
			if owner != (ids.ObjectAddress{}) && provider.removed(owner) {
				target = owner
			}
			// Explicit roots remain distinct; only automatically retired members
			// share the subject of their removed partition ancestry.
			if i >= originalRootCount {
				for {
					relation, ok := provider.retirementDescriptor(target).(*catalog.Relation)
					if !ok || relation.Membership == nil {
						break
					}
					parent := relation.Membership.Parent
					if !provider.removed(parent) {
						break
					}
					target = parent
				}
			}
			if primary == (ids.ObjectAddress{}) {
				primary = target
			} else if primary != target {
				primary = ids.ObjectAddress{}
				break
			}
		}
	}
	if primary != (ids.ObjectAddress{}) {
		message = "cannot drop " + dependencyObject(provider.base, primary, provider.path) + " because other objects depend on it"
	}
	details := make([]string, 0, len(edges))
	for _, edge := range edges {
		if !edge.automatic {
			target := edge.target
			if edge.address.ClassID == 2606 && edge.relation != nil {
				for _, f := range edge.relation.Constraints.ForeignKeys {
					if f.OID == edge.address.ObjectID && edge.root.ClassID == f.Target.ClassID && edge.root.ObjectID == f.Target.ObjectID {
						target = edge.root
						break
					}
				}
			}
			object := dependencyObject(provider.base, target, provider.path)
			if index, ok := provider.retirementDescriptor(target).(*catalog.Relation); ok && index.SQLIndex != nil {
				object = "index " + dependencyRelationName(provider.base, index, provider.path)
			}
			details = append(details, edge.label+" depends on "+object)
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

func (scope *StatementScope) stageDomainDependents(stage *CatalogStage, mode parser.DropMode) error {
	start := len(stage.viewCascadeNames)
	if err := scope.stageExpressionDependents(stage, stage.domainTargets, mode, nil); err != nil {
		return err
	}
	stage.schemaCascadeNames = append(stage.schemaCascadeNames, stage.viewCascadeNames[start:]...)
	stage.viewCascadeNames = stage.viewCascadeNames[:start]
	return nil
}

// All stored-expression owners use the same immutable authority traversal.
// Generated owners are removed through the existing column/storage projection.
func (scope *StatementScope) stageExpressionDependents(stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, changes *session.Journal) error {
	return stageExpressionDependencies(scope.op.Context(), stage, roots, mode, scope, changes, nil, ids.ObjectAddress{})
}

// The same ordered discovery/descriptor core serves SQL, outer Commit and
// drained physical retirement. Only real SQL scopes perform grant acquisition;
// Commit supplies its actual private-root projector without inventing a scope.
// A nonzero subject attributes only the RESTRICT message to one SQL DROP;
// retirement roots, dependency discovery, locks and mutations stay unchanged.
func stageExpressionDependencies(ctx context.Context, stage *CatalogStage, roots []ids.ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *session.Journal, project func(*catalog.Relation, *catalog.Relation) error, subject ids.ObjectAddress) error {
	return stageDropProvider(ctx, stage, scope, changes, project).expressionDependencies(roots, mode, subject)
}

func (provider *dropProvider) expressionDependencies(roots []ids.ObjectAddress, mode parser.DropMode, subject ids.ObjectAddress) error {
	scope := provider.scope
	if len(roots) == 0 {
		return nil
	}
	originalRootCount := len(roots)
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	var retirementRoots []ids.ObjectAddress
	for address, descriptor := range provider.removalDescriptors() {
		if _, relation := descriptor.(*catalog.Relation); relation && !slices.Contains(roots, address) {
			retirementRoots = append(retirementRoots, address)
		}
	}
	if len(retirementRoots) != 0 {
		sort.Slice(retirementRoots, func(i, j int) bool { return retirementRoots[i].ObjectID < retirementRoots[j].ObjectID })
		roots = append(append([]ids.ObjectAddress(nil), roots...), retirementRoots...)
	}
	if err := provider.lockInheritanceClosure(roots); err != nil {
		return err
	}
	edges, err := provider.dependencyClosure(roots)
	if err != nil {
		return err
	}
	visible := false
	for _, edge := range edges {
		visible = visible || !edge.automatic
	}
	if mode == parser.DropRestrict && visible {
		return provider.restrictError(roots, edges, originalRootCount, subject)
	}
	targets := append([]ids.ObjectAddress(nil), roots...)
	var relations []*catalog.Relation
	var hierarchyTargets []ids.ObjectAddress
	seen := make(map[ids.ObjectAddress]bool)
	for _, target := range targets {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			seen[target] = true
		}
	}
	for _, edge := range edges {
		// Discovery can enter an owned provider without emitting a removal
		// edge for it: relation retirement already removes its row type and
		// array. Preserve the captured provider identity when pruning stored
		// expressions, not just the requested root and dependent addresses.
		if !slices.Contains(targets, edge.target) {
			targets = append(targets, edge.target)
		}
		if foreignCatalogClass(edge.address.ClassID) || edge.address.ClassID == ids.OperatorClassID || edge.address.ClassID == ids.TypeClassID || edge.address.ClassID == ids.ExtensionClassID || edge.address.ClassID == ids.ProcedureClassID || edge.address.ClassID == ids.TSTemplateClassID || edge.address.ClassID == ids.TSDictionaryClassID || edge.column != "" || edge.relation != nil && edge.address == edge.relation.Address() {
			targets = append(targets, edge.address)
			if edge.address.ClassID == ids.RelationClassID && edge.address.SubID == 0 {
				seen[edge.address] = true
			}
		}
		if edge.relation != nil && edge.address == edge.relation.Address() && catalog.HierarchyRelation(edge.relation) {
			hierarchyTargets = append(hierarchyTargets, edge.address)
		}
	}
	// The final closure can discover SQL/MED providers below a schema,
	// registered procedure or wrapper root. Fence their incoming identities
	// together with the existing extension retirement observation.
	for _, target := range targets {
		observe := target.ClassID == ids.ExtensionClassID || target.ClassID == ids.ForeignWrapperClassID || target.ClassID == ids.ForeignServerClassID
		if target.ClassID == ids.ProcedureClassID {
			procedure, ok := provider.retirementDescriptor(target).(*catalog.Procedure)
			observe = ok && procedure.Foreign != nil
		}
		if observe {
			provider.observeIdentityDependencies(targets)
			break
		}
	}
	for _, edge := range edges {
		if edge.relation != nil && (edge.column != "" || edge.address.ClassID == 2606) && !seen[edge.relation.Address()] {
			relations = append(relations, edge.relation)
			seen[edge.relation.Address()] = true
		}
	}
	for _, edge := range edges {
		if scope != nil && edge.relation != nil && edge.address == edge.relation.Address() && catalog.HierarchyRelation(edge.relation) {
			if err := scope.acquirePartitionDropLocks(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), edge.address)); err != nil {
				return err
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	e := provider.owner
	if scope != nil {
		// Lock owners are not deletion/projection targets. A policy edge
		// captures its surviving owner's identity, even without a column edge.
		lockRelations := make([]*catalog.Relation, 0, len(relations))
		lockSeen := make(map[ids.ObjectAddress]bool)
		for _, r := range relations {
			lockRelations = append(lockRelations, r)
			lockSeen[r.Address()] = true
		}
		for _, edge := range edges {
			r := edge.relation
			if r == nil || lockSeen[r.Address()] {
				continue
			}
			if edge.address.ClassID == ids.PolicyClassID || edge.address == r.Address() && (r.RelationKind == catalog.TableRelation || r.RelationKind == catalog.ForeignRelation) {
				lockRelations = append(lockRelations, r)
				lockSeen[r.Address()] = true
			}
		}
		sort.Slice(lockRelations, func(i, j int) bool { return lockRelations[i].Address().ObjectID < lockRelations[j].Address().ObjectID })
		for _, r := range lockRelations {
			ref := e.catalog.Domain.Ref(provider.base.Epoch(), r.Address())
			if err := provider.acquire(ref, true); err != nil {
				return err
			}
		}
	}
	// The complete captured whole-relation closure is retirement authority before
	// any surviving DEFAULT/CHECK or column mutation is classified. Keep explicit
	// root removal policy and the original base/edges for discovery, owned objects,
	// diagnostics and publication fences. Type/collation providers retire later,
	// after surviving column candidates have finished binding against them.
	for _, target := range targets[len(roots):] {
		if target.ClassID == ids.RelationClassID && target.SubID == 0 {
			if err = provider.remove(e.catalog.Domain.Ref(provider.base.Epoch(), target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	noticeStart := provider.noticePosition()
	if err = provider.applyViewDependents(targets, parser.DropCascade); err != nil {
		return err
	}
	if err := provider.applyForeignKeyDependents(edges); err != nil {
		return err
	}
	for _, old := range relations {
		if provider.removed(old.Address()) {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, old.Address()) {
			continue
		}
		ref := e.catalog.Domain.Ref(provider.base.Epoch(), old.Address())
		next := old
		consumed := false
		for _, edge := range edges {
			if edge.column == "" || edge.relation.Address() != old.Address() {
				continue
			}
			if provider.removed(old.Address()) {
				break
			}
			if replacement, ok := provider.replacement(old.Address()).(*catalog.Relation); ok {
				next = replacement
			}
			// The closure captured a live stable attribute. A nested cascade
			// may already have consumed it through a different provider path.
			slot := next.IndexForAttribute(edge.address.SubID)
			if slot < 0 {
				continue
			}
			if err = provider.inheritanceAlterGuard(next); err != nil {
				return err
			}
			next, err = ordinaryColumnDrop(provider, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: next.Columns[slot].Name}, Mode: parser.DropCascade})
			if err != nil {
				return err
			}
			consumed = true
		}
		if !consumed {
			continue
		}
		if provider.removed(old.Address()) {
			continue
		}
		revision, er := provider.revision(old.Revision())
		if er != nil {
			return er
		}
		next.ObjectRevision = revision
		if err := provider.projectColumn(old, next); err != nil {
			return err
		}
	}
	for _, edge := range edges {
		if edge.domain == nil {
			continue
		}
		if provider.removed(edge.domain.Address()) {
			continue
		}
		if err := provider.removeDomainCheck(edge.domain, edge.address.ObjectID); err != nil {
			return err
		}
	}
	for _, target := range targets {
		if foreignCatalogClass(target.ClassID) || target.ClassID == ids.OperatorClassID || target.ClassID == ids.TypeClassID || target.ClassID == ids.CollationClassID || target.ClassID == ids.ConversionClassID || target.ClassID == ids.ExtensionClassID || target.ClassID == ids.ProcedureClassID || target.ClassID == ids.TSTemplateClassID || target.ClassID == ids.TSDictionaryClassID {
			if err = provider.remove(e.catalog.Domain.Ref(provider.base.Epoch(), target)); err != nil {
				break
			}
		}
	}
	if err != nil {
		return err
	}
	provider.rewindNotices(noticeStart)
	for _, edge := range edges {
		if !edge.automatic {
			provider.cascadeNotice("drop cascades to " + edge.label)
		}
	}
	// Newly discovered hierarchy owners are not the caller's original roots.
	// Stage their AUTO closure after removing them so recursive expression
	// traversal cannot rediscover the same hierarchy during sequence preparation.
	if len(hierarchyTargets) != 0 {
		return provider.ownedExpressionDependencies(hierarchyTargets, mode)
	}
	return nil
}

func (e *Engine) stageSchemaDomainsLocked(op *session.Operation, auth AuthorizationView, stage *CatalogStage, schemas []*catalog.Namespace, relations []*catalog.Relation, domains []catalog.Descriptor, cascade bool) error {
	var contained []catalog.Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.domainTargets = append(stage.domainTargets, d.Address())
		if extension, ok := d.(*catalog.Extension); ok {
			for _, member := range extension.Members {
				stage.domainTargets = append(stage.domainTargets, member)
				if err := stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), member), false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.domainTargets, func(i, j int) bool { return stage.domainTargets[i].ObjectID < stage.domainTargets[j].ObjectID })
	stage.dependencyPath, _ = e.referencePath(auth)
	for _, d := range contained {
		if err := stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), false); err != nil {
			return err
		}
	}
	edges, err := domainDependencyClosure(op.Context(), stage, stage.domainTargets)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := dependencyObject(stage.Base, d.Address(), stage.dependencyPath)
		namespaceAddress := d.Namespace()
		if extension, ok := d.(*catalog.Extension); ok {
			namespaceAddress = extension.TargetNamespace
		}
		namespace := stage.Base.Get(namespaceAddress).Name()
		details = append(details, label+" depends on schema "+catalog.DependencyIdentifier(namespace))
		stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			root := edge.root == d.Address()
			if extension, ok := d.(*catalog.Extension); ok {
				root = root || slices.Contains(extension.Members[:], edge.root)
			}
			if root && !edge.automatic {
				details = append(details, edge.label+" depends on "+dependencyObject(stage.Base, edge.target, stage.dependencyPath))
			}
		}
	}
	if !cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + catalog.DependencyIdentifier(schemas[0].Name()) + " because other objects depend on it"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	for _, d := range schemas {
		if err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.Base.Epoch(), d.Address()), true); err != nil {
			return err
		}
	}
	return nil
}

func (stage *CatalogStage) removeDomainCheck(d *catalog.Type, oid ids.OID) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removeDomainCheck(d, oid)
}

func (provider *dropProvider) removeDomainCheck(d *catalog.Type, oid ids.OID) error {
	if replacement, ok := provider.replacement(d.Address()).(*catalog.Type); ok {
		d = replacement
	}
	next := *d
	next.DomainChecks = make([]catalog.DomainCheckConstraint, 0, len(d.DomainChecks))
	for _, check := range d.DomainChecks {
		if check.OID != oid {
			next.DomainChecks = append(next.DomainChecks, check)
		}
	}
	var err error
	next.SQLType, err = sqltype.Domain(d.SQLType.ID(), d.Name(), d.SQLType.SQLName(), d.SQLType.DomainBase(), len(next.DomainChecks) != 0, d.SQLType.OwnDomainNotNull())
	if err != nil {
		return err
	}
	next.ObjectRevision, err = provider.revision(d.ObjectRevision)
	if err != nil {
		return err
	}
	return provider.replace(&next)
}
