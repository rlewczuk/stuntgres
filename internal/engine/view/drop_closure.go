package view

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqlstate"
)

// DropClosure returns the dependents of targets in r, depth first: every
// edge with the target it was reached from, its root and its diagnostic
// label. PostgreSQL dependency traversal visits a dependent domain's
// descendants before resuming its parent's remaining dependents.
func DropClosure(r DropReader, targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	ctx, base, path := r.Context(), r.BaseRead(), r.Path()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// An explicit hard-member deletion cannot bypass its class owner. A
	// containing owner already in the retirement roots legitimately consumes it.
	for _, target := range targets {
		owner := catalog.OperatorFamilyMemberOwner(r.RetirementDescriptor(target))
		if owner != (ids.ObjectAddress{}) && !slices.Contains(targets, owner) && !r.Removed(owner) {
			object := stored.DependencyObject(base, target, path)
			owning := stored.DependencyObject(base, owner, path)
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because " + owning + " requires it", Hint: "You can drop " + owning + " instead."}
		}
	}
	domains := make(map[ids.ObjectAddress]bool, len(targets))
	for _, a := range targets {
		domains[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a, d := range r.InputDescriptors() {
			if t, ok := d.(*catalog.Type); ok && !domains[a] && (t.SQLType.IsDomain() && (domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Type.ID())}] || domains[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Collation.ID)}]) || t.SQLType.IsArray() && domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.Element().ID())}]) {
				domains[a] = true
				changed = true
			}
		}
	}
	droppedColumns := make(map[ids.ObjectAddress]bool)
	for a, d := range r.InputDescriptors() {
		relation, ok := d.(*catalog.Relation)
		if !ok || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && relation.RelationKind != catalog.CompositeRelation && relation.RelationKind != catalog.ForeignRelation {
			continue
		}
		for i, c := range relation.Columns {
			if domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}] || domains[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(c.Usage.Collation.ID)}] {
				column := a
				column.SubID = relation.Attributes[i]
				droppedColumns[column] = true
			}
		}
	}
	seen := make(map[ids.ObjectAddress]bool)
	for _, a := range targets {
		seen[a] = true
	}
	var result []table.DomainDependency
	resultSlots := make(map[ids.ObjectAddress]int)
	// Domain roots and composite-attribute roots with real domain-default
	// edges use the catalog's descending visitation/reverse-postorder walk.
	// Other provider routes retain their established traversal contracts.
	compositeAttributeRoot := false
	if len(targets) == 1 && targets[0].ClassID == ids.RelationClassID && targets[0].SubID > 0 {
		address := targets[0]
		address.SubID = 0
		relation, ok := r.Descriptor(address).(*catalog.Relation)
		compositeAttributeRoot = ok && relation.RelationKind == catalog.CompositeRelation && relation.IndexForAttribute(targets[0].SubID) >= 0
	}
	domainRootOrder := false
	for _, target := range targets {
		if typ, ok := r.RetirementDescriptor(target).(*catalog.Type); ok && typ.SQLType.IsDomain() {
			domainRootOrder = true
			break
		}
	}
	compositeDefaultOrder := false
	var postorder []int
	mergedDescriptors := r.DescriptorCount() != base.DescriptorCount()
	var visit func(ids.ObjectAddress, ids.ObjectAddress) error
	visit = func(target, root ids.ObjectAddress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relation, ok := r.RetirementDescriptor(target).(*catalog.Relation); ok && relation.RowType != 0 {
			if err := visit(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}, root); err != nil {
				return err
			}
			row, ok := r.RetirementDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)}).(*catalog.Type)
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
		if relation, ok := r.RetirementDescriptor(target).(*catalog.Relation); ok && relation.Partition != nil {
			for _, child := range relation.Partition.Children {
				if r.Removed(child) {
					if err := visit(child, root); err != nil {
						return err
					}
				}
			}
		}
		var direct []table.DomainDependency
		for a, descriptor := range r.Descriptors() {
			if mergedDescriptors {
				if replacement := r.Replacement(a); replacement != nil {
					descriptor = replacement
				}
			}
			// Owned indexes are AUTO dependents of the table and every referenced
			// stable attribute. Retire them in this closure, before their column
			// types disappear and before generation reownership sees live indexes.
			// Already removed indexes still expose their external consumers.
			if idx, ok := descriptor.(*catalog.Relation); ok && idx.SQLIndex != nil && idx.SQLIndex.Parent.ClassID == target.ClassID && idx.SQLIndex.Parent.ObjectID == target.ObjectID && (target.SubID == 0 || index.UniqueIndexTypeAffected(idx.SQLIndex, []index.ColumnTransformation{{Attribute: target.SubID}})) {
				direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true, Relation: idx})
				continue
			}
			if r.Removed(a) {
				continue
			}
			if target.ClassID == ids.NamespaceClassID && descriptor.Namespace() == target && access.OwnershipRoot(base, descriptor, routine.OperatorSeeded) {
				edge := table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)}
				edge.Relation, _ = descriptor.(*catalog.Relation)
				direct = append(direct, edge)
				continue
			}
			switch d := descriptor.(type) {
			case *catalog.AccessMethod:
				for _, dependency := range catalog.AccessMethodDependencies(d) {
					if dependency.Address == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						break
					}
				}
			case *catalog.OperatorClass, *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
				for _, dependency := range catalog.OperatorFamilyDependencies(base, d) {
					if dependency.Address == target {
						address := a
						automatic := dependency.Kind == 'a' || dependency.Kind == 'i'
						// Reaching a hard member through its implementation/type
						// means deleting its owning class, not detaching the member.
						// Traversing that class then discovers all INTERNAL members
						// and its external consumers through the ordinary closure.
						if owner := catalog.OperatorFamilyMemberOwner(d); owner != (ids.ObjectAddress{}) && dependency.Kind == 'n' {
							address = owner
						}
						direct = append(direct, table.DomainDependency{Address: address, Target: target, Root: root, OID: address.ObjectID, Automatic: automatic, Label: stored.DependencyObject(base, address, path)})
						break
					}
				}
			case *catalog.Operator:
				if routine.OperatorDependsOn(base, d, target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
				}
			case *catalog.ForeignObject:
				for _, dependency := range pgcatalog.ForeignDependencies(d) {
					if dependency == (ids.ObjectAddress{}) {
						continue
					}
					if dependency == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						break
					}
				}
			case *catalog.Extension:
				if d.TargetNamespace == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
				}
				required, err := catalog.ExtensionPrerequisites(base, d)
				if err != nil {
					return err
				}
				for _, prerequisite := range required {
					if prerequisite == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					}
				}
			case *catalog.Procedure:
				for dependency := range catalog.OwnedAggregateDependencies(d) {
					if dependency == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						break
					}
				}
				if d.SQLBody != nil {
					for _, dependency := range d.SQLBody.Dependencies() {
						address := dependency.Ref.Address
						if address == target || target.SubID == 0 && address.ClassID == target.ClassID && address.ObjectID == target.ObjectID {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
							break
						}
					}
				}
				if d.SQLDefaults != nil {
					for _, dependency := range d.SQLDefaults.Dependencies() {
						address := dependency.Ref.Address
						if address == target || target.SubID == 0 && address.ClassID == target.ClassID && address.ObjectID == target.ObjectID {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
							break
						}
					}
				}
				if d.SQL != nil && target.ClassID == ids.TypeClassID {
					uses := d.Result == target.ObjectID
					for _, formal := range d.SQL.Formals {
						uses = uses || ids.OID(formal.Usage.Type.ID()) == target.ObjectID
					}
					if uses {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					}
				}
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.TSDictionary:
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.TSTemplate:
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.Type:
				if replacement, ok := r.Replacement(a).(*catalog.Type); ok {
					d = replacement
				}
				if !domains[a] {
					for _, check := range d.DomainChecks {
						for _, dep := range check.Dependencies {
							if dep.Ref.Address == target {
								direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}, Target: target, Root: root, OID: check.OID, Label: "constraint " + catalog.DependencyIdentifier(check.Name), Domain: d})
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
					direct = append(direct, table.DomainDependency{Address: a, Target: dependencyTarget, Root: root, OID: a.ObjectID, Label: "type " + catalog.TypeDiagnosticNameAt(base, d, path)})
				}
				if d.SQLType.IsArray() && target == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.SQLType.Element().ID())}) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.Relation:
				if replacement, ok := r.Replacement(a).(*catalog.Relation); ok {
					d = replacement
				}
				if d.Foreign != nil && d.Foreign.Server == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path), Relation: d})
				}
				if d.RelationKind == catalog.CompositeRelation && target == (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)}) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Relation: d, Automatic: true})
					continue
				}
				if MaterializedExtensionDepends(d, target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Relation: d, Automatic: true, Label: "materialized view " + stored.DependencyRelationName(base, d, path)})
				}
				if d.Membership != nil && d.Membership.Parent == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Relation: d, Automatic: true})
				}
				if catalog.PartitionKeyDependsOn(d, target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Relation: d, Label: "table " + stored.DependencyRelationName(base, d, path)})
					continue
				}
				if d.SQLIndex != nil {
					depends, automatic := false, false
					for _, key := range d.SQLIndex.Keys {
						depends = depends || target == (ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(key.Collation)})
						depends = depends || target == (ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(key.Class.OID())})
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
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "index " + stored.DependencyRelationName(base, d, path), Relation: d})
					}
					continue
				}
				var name string
				for _, rule := range stored.RulesOf(d) {
					if RuleDepends(base, rule, target) {
						if name == "" {
							name = stored.DependencyRelationName(base, d, path)
						}
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2618, ObjectID: rule.OID}, Target: target, Root: root, OID: rule.OID, Label: "rule " + catalog.DependencyIdentifier(rule.Name) + " on " + catalog.RelationDiagnosticKind(d) + " " + name, Relation: d})
					}
				}
				if d.View != nil {
					if ViewDepends(d, target) {
						passive := false
						if compositeDefaultOrder || domainRootOrder {
							var err error
							passive, err = PassiveTypeDependency(r, d, target)
							if err != nil {
								return err
							}
						}
						// Lifetime captures retain selected-column datatypes,
						// but only explicit SQL type references own a direct
						// type-to-view dependency. Passive captures must not
						// preempt the real output/source-column owner paths.
						if !passive {
							if name == "" {
								name = stored.DependencyRelationName(base, d, path)
							}
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: catalog.RelationDiagnosticKind(d) + " " + name, Relation: d})
						}
					}
					// Output columns have their own datatype dependencies, just
					// like table columns. Visiting them can reach another view's
					// _RETURN owner before that owner's source table is visited.
					// Do not flatten these paths into a whole-view type edge.
					if domainRootOrder {
						for i, c := range d.Columns {
							if typedef.UsageDependsOn(c.Usage, target) {
								column := a
								column.SubID = d.Attributes[i]
								direct = append(direct, table.DomainDependency{Address: column, Target: target, Root: root, OID: a.ObjectID, Ordinal: column.SubID, Label: "column " + catalog.DependencyIdentifier(c.Name) + " of " + catalog.RelationDiagnosticKind(d) + " " + stored.DependencyRelationName(base, d, path), Relation: d, Column: c.Name})
							}
						}
					}
					continue
				}
				if d.RelationKind != catalog.TableRelation && d.RelationKind != catalog.PartitionedRelation && d.RelationKind != catalog.CompositeRelation && d.RelationKind != catalog.ForeignRelation {
					continue
				}
				if name == "" {
					name = stored.DependencyRelationName(base, d, path)
				}
				if d.OfType == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "table " + name, Relation: d})
					continue
				}
				if d.Inheritance != nil {
					for _, parent := range d.Inheritance.Parents {
						if parent.Relation == target {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "table " + name, Relation: d})
							break
						}
					}
				}
				for _, policy := range d.Security.Policies {
					if PolicyDepends(policy, target) {
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}, Target: target, Root: root, OID: policy.OID, Label: "policy " + catalog.DependencyIdentifier(policy.Name) + " on table " + name, Relation: d})
					}
				}
				for i, c := range d.Columns {
					column := a
					column.SubID = d.Attributes[i]
					if typedef.UsageDependsOn(c.Usage, target) {
						direct = append(direct, table.DomainDependency{Address: column, Target: target, Root: root, OID: a.ObjectID, Ordinal: column.SubID, Label: "column " + catalog.DependencyIdentifier(c.Name) + " of " + catalog.RelationDiagnosticKind(d) + " " + name, Relation: d, Column: c.Name})
					}
					if droppedColumns[column] {
						continue
					}
					if definition, ok := d.ColumnMeta.Defaults[column.SubID]; ok {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}, Target: target, Root: root, OID: definition.OID, Label: "default value for column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, Relation: d})
								break
							}
						}
					}
					if definition, generated := d.ColumnMeta.Generations[column.SubID]; generated && target != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}) {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								direct = append(direct, table.DomainDependency{Address: column, Target: target, Root: root, OID: a.ObjectID, Ordinal: column.SubID, Label: "column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, Relation: d, Column: c.Name})
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
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}, Target: target, Root: root, OID: orderOID, Ordinal: ordinal, Label: "constraint " + catalog.DependencyIdentifier(check.Name) + " on table " + name, Relation: d})
					}
				}
				for _, f := range d.Constraints.ForeignKeys {
					depends := target == f.Index || target == f.Target
					if target.ClassID == f.Target.ClassID && target.ObjectID == f.Target.ObjectID && target.SubID != 0 {
						depends = slices.Contains(f.Referenced, target.SubID)
					}
					automatic := target.ClassID == d.Address().ClassID && target.ObjectID == d.Address().ObjectID && (target.SubID == 0 || slices.Contains(f.Attributes, target.SubID))
					if depends || automatic {
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2606, ObjectID: f.OID}, Target: target, Root: root, OID: f.OID, Label: "constraint " + catalog.DependencyIdentifier(f.Name) + " on table " + name, Relation: d, Automatic: automatic})
					}
				}
			}
		}
		sort.Slice(direct, func(i, j int) bool {
			if compositeDefaultOrder {
				if direct[i].Address.ObjectID != direct[j].Address.ObjectID {
					return direct[i].Address.ObjectID > direct[j].Address.ObjectID
				}
				if direct[i].Address.ClassID != direct[j].Address.ClassID {
					return direct[i].Address.ClassID < direct[j].Address.ClassID
				}
				return direct[i].Address.SubID < direct[j].Address.SubID
			}
			if domainRootOrder {
				if direct[i].OID != direct[j].OID {
					return direct[i].OID > direct[j].OID
				}
				if direct[i].Address.ClassID != direct[j].Address.ClassID {
					return direct[i].Address.ClassID < direct[j].Address.ClassID
				}
				return direct[i].Ordinal < direct[j].Ordinal
			}
			if direct[i].OID != direct[j].OID {
				return direct[i].OID < direct[j].OID
			}
			// Columns and relation-target CHECKs share their table's primary
			// identity. Order those real dependent classes before their own
			// stable attribute/declaration ordinals, not CHECK enforcement OIDs.
			if direct[i].Address.ClassID != direct[j].Address.ClassID {
				return direct[i].Address.ClassID < direct[j].Address.ClassID
			}
			return direct[i].Ordinal < direct[j].Ordinal
		})
		for _, edge := range direct {
			if seen[edge.Address] {
				if slot, exists := resultSlots[edge.Address]; exists && edge.Automatic && !result[slot].Automatic {
					result[slot].Automatic = true
					result[slot].Target = edge.Target
					result[slot].Root = edge.Root
				}
				continue
			}
			seen[edge.Address] = true
			slot := len(result)
			resultSlots[edge.Address] = len(result)
			result = append(result, edge)
			if edge.Address.ClassID == ids.OperatorClassCatalogID || edge.Address.ClassID == ids.OperatorFamilyClassID || edge.Address.ClassID == ids.TypeClassID || edge.Address.ClassID == ids.RelationClassID || edge.Address.ClassID == ids.ExtensionClassID || edge.Address.ClassID == ids.ProcedureClassID || edge.Address.ClassID == ids.TSTemplateClassID || edge.Address.ClassID == ids.TSDictionaryClassID || edge.Address.ClassID == ids.ForeignWrapperClassID || edge.Address.ClassID == ids.ForeignServerClassID || edge.Address.ClassID == ids.ForeignMappingClassID {
				if err := visit(edge.Address, root); err != nil {
					return err
				}
			}
			if compositeDefaultOrder || domainRootOrder {
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
	if compositeDefaultOrder || domainRootOrder {
		// Keep original slots throughout DFS so repeated/automatic edges use
		// the same authority. Then apply reverse postorder in place, without
		// a second dependency graph or duplicate descriptor/edge buffers.
		for position, slot := range postorder {
			resultSlots[result[slot].Address] = len(result) - 1 - position
		}
		for i := range result {
			for destination := resultSlots[result[i].Address]; destination != i; destination = resultSlots[result[i].Address] {
				result[i], result[destination] = result[destination], result[i]
			}
		}
	}
	// A retired whole view subsumes its output columns. Keep their traversal
	// authority, but do not report or mutate them as independently surviving
	// columns. This is the same subobject rule as the catalog dependency walk.
	for i := range result {
		edge := &result[i]
		if edge.Relation != nil && edge.Relation.View != nil && edge.Address.SubID != 0 {
			if _, retired := resultSlots[edge.Relation.Address()]; retired || r.Removed(edge.Relation.Address()) {
				edge.Automatic = true
			}
		}
	}
	return result, nil
}

// RestrictError is the RESTRICT error for a DROP of targets with the
// dependency edges of DropClosure. A nonzero subject names the dropped
// object; otherwise a single primary target, if any, is named.
func RestrictError(r DropReader, targets []ids.ObjectAddress, edges []table.DomainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	base, path := r.BaseRead(), r.Path()
	message := "cannot drop desired object(s) because other objects depend on them"
	primary := subject
	if primary == (ids.ObjectAddress{}) {
		for i, target := range targets {
			var owner ids.ObjectAddress
			switch descriptor := r.RetirementDescriptor(target).(type) {
			case *catalog.Type:
				owner = descriptor.OwningRelation
			case *catalog.Relation:
				if descriptor.Sequence != nil {
					owner = descriptor.Sequence.Owned
					owner.SubID = 0
				}
			}
			if owner != (ids.ObjectAddress{}) && r.Removed(owner) {
				target = owner
			}
			// Explicit roots remain distinct; only automatically retired members
			// share the subject of their removed partition ancestry.
			if i >= originalRootCount {
				for {
					relation, ok := r.RetirementDescriptor(target).(*catalog.Relation)
					if !ok || relation.Membership == nil {
						break
					}
					parent := relation.Membership.Parent
					if !r.Removed(parent) {
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
		message = "cannot drop " + stored.DependencyObject(base, primary, path) + " because other objects depend on it"
	}
	// An extension reported as a dependent subsumes its owned implementation
	// members in DETAIL. Keep all closure edges for enforcement and retirement;
	// this changes presentation only, not which objects block or follow DROP.
	var reportedExtensions map[ids.ObjectAddress]bool
	for _, edge := range edges {
		if !edge.Automatic && edge.Address.ClassID == ids.ExtensionClassID {
			if reportedExtensions == nil {
				reportedExtensions = make(map[ids.ObjectAddress]bool)
			}
			reportedExtensions[edge.Address] = true
		}
	}
	details := make([]string, 0, len(edges))
	for _, edge := range edges {
		if !edge.Automatic {
			if len(reportedExtensions) != 0 && reportedExtensions[catalog.ExtensionMembership(r.RetirementDescriptor(edge.Address))] {
				continue
			}
			target := edge.Target
			if edge.Address.ClassID == 2606 && edge.Relation != nil {
				for _, f := range edge.Relation.Constraints.ForeignKeys {
					if f.OID == edge.Address.ObjectID && edge.Root.ClassID == f.Target.ClassID && edge.Root.ObjectID == f.Target.ObjectID {
						target = edge.Root
						break
					}
				}
			}
			object := stored.DependencyObject(base, target, path)
			if index, ok := r.RetirementDescriptor(target).(*catalog.Relation); ok && index.SQLIndex != nil {
				object = "index " + stored.DependencyRelationName(base, index, path)
			}
			details = append(details, edge.Label+" depends on "+object)
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
}

// PassiveTypeDependency reports whether relation, a view, depends on the
// domain target only through a passive capture. Lifetime captures include a
// selected column's datatype. SQL dependency recording does not turn that
// passive capture into a direct type-to-view edge. Domain-root and
// composite-default walks instead reach such a view through its genuine
// retiring columns; explicit SQL datatype references remain direct.
func PassiveTypeDependency(r DropReader, relation *catalog.Relation, target ids.ObjectAddress) (bool, error) {
	base := r.BaseRead()
	if target.ClassID != ids.TypeClassID {
		return false, nil
	}
	domain, ok := r.Descriptor(target).(*catalog.Type)
	if !ok || !domain.SQLType.IsDomain() {
		return false, nil
	}
	storedView := stored.ViewOf(relation)
	if storedView == nil || storedView.Query() == nil {
		return false, nil
	}
	column := false
	for _, dependency := range storedView.Dependencies() {
		ref := dependency.Ref
		if ref.Address.ClassID != ids.RelationClassID || ref.Address.SubID <= 0 || ref.Domain() != base.Owner().Domain || ref.Epoch != base.Epoch() {
			continue
		}
		address := ref.Address
		address.SubID = 0
		source, ok := r.Descriptor(address).(*catalog.Relation)
		if !ok {
			continue
		}
		slot := source.IndexForAttribute(ref.Address.SubID)
		if slot >= 0 && typedef.UsageDependsOn(source.Columns[slot].Usage, target) {
			column = true
			break
		}
	}
	if !column {
		return false, nil
	}
	reader := r.DefinitionInput()
	if reader == nil {
		return false, fmt.Errorf("stuntgres: missing view dependency projection")
	}
	refs, err := stored.QueryDependRefs(r.Context(), base, reader, storedView.Query())
	if err != nil {
		return false, err
	}
	return !slices.Contains(refs, target), nil
}
