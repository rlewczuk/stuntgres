package view

import (
	"fmt"
	"iter"
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

// DropClosure returns each dependent once, retaining its actual reached target,
// root and diagnostic label. Attribute, procedure-only and domain root sets use
// descending discovery and reverse postorder.
func DropClosure(r DropReader, targets []ids.ObjectAddress) ([]table.DomainDependency, error) {
	ctx, base, path := r.Context(), r.BaseRead(), r.Path()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Codec facts must read one owner-supplied graph, not pair an evolved
	// descriptor with its predecessor catalog. Keep original AUTO payload
	// discovery below separate from this retained metadata projection.
	var retirementRead catalog.Read
	typeRead := func() catalog.Read {
		if retirementRead.State() == nil {
			retirementRead = r.RetirementRead()
		}
		return retirementRead
	}
	// An explicit extension or hard-member deletion cannot bypass its owner,
	// even under CASCADE. A containing owner already retiring consumes it.
	for _, target := range targets {
		// A pinned target is rejected before inspecting extension ownership,
		// including a seeded parser with a lawful mutable e-link.
		if stored.DependPinned(base, r.DefinitionInput(), target) {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + stored.DependencyObject(base, target, path) + " because it is required by the database system"}
		}
		descriptor := r.RetirementDescriptor(target)
		var owner ids.ObjectAddress
		if target.SubID == 0 {
			owner = catalog.ExtensionMembership(descriptor)
		}
		if owner == (ids.ObjectAddress{}) {
			owner = catalog.OperatorFamilyMemberOwner(descriptor)
		}
		if owner != (ids.ObjectAddress{}) && !slices.Contains(targets, owner) && !r.Removed(owner) {
			object := stored.DependencyObject(base, target, path)
			owning := stored.DependencyObject(base, owner, path)
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + object + " because " + owning + " requires it", Hint: "You can drop " + owning + " instead."}
		}
	}
	// While every domain-set member is newer than the seed, no seeded
	// descriptor can depend on one, so the input walks skip seeded entries.
	domains := make(map[ids.ObjectAddress]bool, len(targets))
	newer := true
	procedureTargets := false
	for _, a := range targets {
		domains[a] = true
		newer = newer && !r.SeedMayReference(a)
		procedureTargets = procedureTargets || a.ClassID == ids.ProcedureClassID
	}
	input := func() iter.Seq2[ids.ObjectAddress, catalog.Descriptor] {
		if newer {
			return r.UnseededInputDescriptors()
		}
		return r.InputDescriptors()
	}
	for changed := true; changed; {
		changed = false
		for a, d := range input() {
			t, ok := d.(*catalog.Type)
			if !ok || domains[a] {
				continue
			}
			depends := t.SQLType.IsDomain() && (domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Type.ID())}] || domains[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(t.SQLType.DomainBase().Collation.ID)}]) || t.SQLType.IsArray() && domains[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.Element().ID())}]
			for _, dependency := range catalog.RangeDependencies(t) {
				depends = depends || domains[dependency]
			}
			if !depends && procedureTargets && !stored.DependPinned(typeRead(), nil, a) {
				read := typeRead()
				if err := pgcatalog.VisitTypeProcedureDependencies(ctx, read, read.MustType(a), func(dependency ids.ObjectAddress) bool {
					depends = domains[dependency]
					return !depends
				}); err != nil {
					return nil, err
				}
			}
			if depends {
				domains[a] = true
				changed = true
				newer = newer && !r.SeedMayReference(a)
			}
		}
	}
	droppedColumns := make(map[ids.ObjectAddress]bool)
	for a, d := range input() {
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
	// One local address state retains early provenance even if its effect is
	// admitted only by a later AUTO reach. It owns no catalog authority.
	type visitState struct {
		expanded bool
		normal   bool
	}
	visits := make(map[ids.ObjectAddress]visitState)
	markNormal := func(address ids.ObjectAddress) {
		state := visits[address]
		state.normal = true
		visits[address] = state
		if slot, exists := resultSlots[address]; exists {
			result[slot].Normal = true
		}
		if recorder, ok := r.(interface {
			MarkDrop(ids.ObjectAddress, catalog.DropProvenance)
		}); ok {
			recorder.MarkDrop(address, catalog.DropProvenance{Normal: true})
		}
	}
	// Attribute roots, domain roots and procedure-only root sets use descending
	// visitation/reverse postorder. Besides ordering the output, this chooses the
	// actual direct parent when a shared descendant is reachable by several paths.
	columnRootOrder := len(targets) == 1 && targets[0].ClassID == ids.RelationClassID && targets[0].SubID > 0
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
	// A containing owner's mixed retirement roots retain that provider's
	// traversal; member procedures do not turn it into a procedure DROP.
	procedureRootOrder := len(targets) != 0
	for _, target := range targets {
		if target.ClassID != ids.ProcedureClassID {
			procedureRootOrder = false
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
		// Pin admission belongs to discovery, before either RESTRICT reporting
		// or CASCADE effects. Use the current definition input: a bootstrap
		// object can acquire a genuine dependency on a user provider.
		if stored.DependPinned(base, r.DefinitionInput(), target) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + stored.DependencyObject(base, target, path) + " because it is required by the database system"}
		}
		// Incoming flags are merged by the caller before this expansion guard.
		// Root/effect membership does not imply that outgoing edges were read.
		state := visits[target]
		if state.expanded {
			return nil
		}
		state.expanded = true
		visits[target] = state
		if relation, ok := r.RetirementDescriptor(target).(*catalog.Relation); ok && target.SubID == 0 && r.Removed(target) && relation.RowType != 0 {
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
		// Column effects need sequence descendants in this same preflight.
		// Whole-provider/whole-target pairs keep their explicit later owned
		// phase, including whole retirement roots added at the boundary.
		columnTraversal := target.ClassID == ids.RelationClassID && target.SubID > 0 || root.ClassID == ids.RelationClassID && root.SubID > 0
		if t, ok := r.RetirementDescriptor(target).(*catalog.Type); ok && t.SQLType.IsMultirange() && t.SQLType.IsUserDefined() {
			owner := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.SQLType.RangeType().ID())}
			// INTERNAL reversal only redirects an independently reached member.
			// Returning from an owner's own member must not grant that owner's
			// incoming NORMAL edge automatic deletion permission.
			if !seen[owner] {
				direct = append(direct, table.DomainDependency{Address: owner, Target: target, Root: root, OID: owner.ObjectID, Automatic: true, Label: stored.DependencyObject(base, owner, path)})
			}
		}
		// Every edge below needs a stored reference to target (or target is the
		// descriptor itself), which no seeded descriptor holds for a newer one.
		dependents := r.Descriptors()
		if !r.SeedMayReference(target) {
			dependents = r.UnseededDescriptors()
		}
		for a, descriptor := range dependents {
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
			if sequence, ok := descriptor.(*catalog.Relation); ok && columnTraversal && table.SequenceOwnedBy(sequence, target) {
				direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true, Relation: sequence})
			}
			if target.ClassID == ids.NamespaceClassID && descriptor.Namespace() == target && access.OwnershipRoot(base, descriptor, routine.OperatorSeeded) {
				edge := table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)}
				edge.Normal = true
				edge.Relation, _ = descriptor.(*catalog.Relation)
				direct = append(direct, edge)
				continue
			}
			if membership := catalog.ExtensionMembership(descriptor); membership != (ids.ObjectAddress{}) && membership == target {
				direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				continue
			}
			switch d := descriptor.(type) {
			case *catalog.Transform:
				d.Dependencies(func(address ids.ObjectAddress) bool {
					if address == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Normal: true, Label: stored.DependencyObject(base, a, path)})
					}
					return true
				})
			case *catalog.Statistics:
				d.VisitDependencies(func(dependency ids.ObjectAddress, kind byte) {
					matches := dependency == target || target.SubID == 0 && dependency.ClassID == target.ClassID && dependency.ObjectID == target.ObjectID
					if matches {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: kind == 'a', Normal: kind == 'n', Label: stored.DependencyObject(base, a, path)})
					}
				})
			case *catalog.Language:
				if d.Procedural && (d.Call == target || d.Inline == target || d.Validator == target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Normal: true, Label: stored.DependencyObject(base, a, path)})
				}
			case *catalog.EventTrigger:
				if d.Function.Address == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
			case *catalog.AccessMethod:
				for _, dependency := range catalog.AccessMethodDependencies(d) {
					if dependency.Address == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						direct[len(direct)-1].Normal = true
						break
					}
				}
			case *catalog.Cast:
				for _, dependency := range catalog.CastDependencies(d) {
					if dependency == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: catalog.CastDependencyKind(d) == 'i', Label: stored.DependencyObject(base, a, path)})
						direct[len(direct)-1].Normal = catalog.CastDependencyKind(d) == 'n'
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
						direct[len(direct)-1].Normal = dependency.Kind == 'n'
						break
					}
				}
			case *catalog.Operator:
				if routine.OperatorDependsOn(base, d, target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
			case *catalog.ForeignObject:
				for _, dependency := range pgcatalog.ForeignDependencies(d) {
					if dependency == (ids.ObjectAddress{}) {
						continue
					}
					if dependency == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						direct[len(direct)-1].Normal = true
						break
					}
				}
			case *catalog.Extension:
				if d.TargetNamespace == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
				required, err := catalog.ExtensionPrerequisites(base, d)
				if err != nil {
					return err
				}
				for _, prerequisite := range required {
					if prerequisite == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						direct[len(direct)-1].Normal = true
					}
				}
			case *catalog.Procedure:
				if owner := catalog.RangeConstructorOwner(d); owner == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
				catalog.VisitProcedureDefinitionDependencies(base, d, func(address ids.ObjectAddress) bool {
					if address == target || target.SubID == 0 && address.ClassID == target.ClassID && address.ObjectID == target.ObjectID {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
						direct[len(direct)-1].Normal = true
						return false
					}
					return true
				})
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.TSParser:
				if slices.Contains(d.Dependencies(), target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
			case *catalog.TSConfiguration:
				if slices.Contains(d.Dependencies(), target) {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
			case *catalog.TSDictionary:
				if d.Template == target || d.Namespace() == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.TSTemplate:
				if replacement, ok := r.Replacement(a).(*catalog.TSTemplate); ok {
					d = replacement
				}
				if d.Namespace() == target || d.Init == target || d.Lexize == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: stored.DependencyObject(base, a, path)})
					direct[len(direct)-1].Normal = true
				}
				if d.Extension == target {
					direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: true})
				}
			case *catalog.Type:
				if replacement, ok := r.Replacement(a).(*catalog.Type); ok {
					d = replacement
				}
				if target.ClassID == ids.ProcedureClassID && !stored.DependPinned(typeRead(), nil, a) {
					read := typeRead()
					if err := pgcatalog.VisitTypeProcedureDependencies(ctx, read, read.MustType(a), func(dependency ids.ObjectAddress) bool {
						if dependency == target {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "type " + catalog.TypeDiagnosticNameAt(base, nil, d, path), Normal: true})
							return false
						}
						return true
					}); err != nil {
						return err
					}
				}
				for _, dependency := range catalog.RangeDependencies(d) {
					if dependency == target {
						direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Automatic: d.SQLType.IsMultirange(), Label: "type " + catalog.TypeDiagnosticNameAt(base, nil, d, path)})
						direct[len(direct)-1].Normal = !d.SQLType.IsMultirange()
					}
				}
				for _, check := range d.DomainChecks {
					for _, dep := range check.Dependencies {
						if dep.Ref.Address == target {
							address := ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}
							markNormal(address)
							if !domains[a] {
								direct = append(direct, table.DomainDependency{Address: address, Target: target, Root: root, OID: check.OID, Label: "constraint " + catalog.DependencyIdentifier(check.Name), Domain: d, Normal: true})
							}
							break
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
					direct = append(direct, table.DomainDependency{Address: a, Target: dependencyTarget, Root: root, OID: a.ObjectID, Label: "type " + catalog.TypeDiagnosticNameAt(base, nil, d, path)})
					direct[len(direct)-1].Normal = true
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
					direct[len(direct)-1].Normal = true
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
					direct[len(direct)-1].Normal = true
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
					if depends {
						// Same-index relation references are AUTO; external
						// expression/type providers are independently NORMAL.
						normal := target.ClassID != d.SQLIndex.Parent.ClassID || target.ObjectID != d.SQLIndex.Parent.ObjectID
						if normal {
							markNormal(a)
						}
						if !automatic {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "index " + stored.DependencyRelationName(base, d, path), Relation: d, Normal: normal})
						}
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
						direct[len(direct)-1].Normal = true
					}
				}
				for _, trigger := range stored.TriggersOf(d) {
					p := trigger.Parts()
					automatic := p.Relation.Address == target || p.Parent == target || slices.Contains(p.AutoExtensions, target)
					if automatic || stored.TriggerDepends(trigger, target) {
						if name == "" {
							name = stored.DependencyRelationName(base, d, path)
						}
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2620, ObjectID: trigger.OID()}, Target: target, Root: root, OID: trigger.OID(), Automatic: automatic, Label: "trigger " + catalog.DependencyIdentifier(trigger.Name()) + " on table " + name, Relation: d})
						direct[len(direct)-1].Normal = stored.TriggerDepends(trigger, target) && p.Relation.Address != target && p.Parent != target
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
							direct[len(direct)-1].Normal = true
							// The NORMAL edge is on _RETURN; its INTERNAL owner
							// reverses to this view. Namespace/AUTO view reaches
							// do not confer the rule's independent NORMAL flag.
							markNormal(ids.ObjectAddress{ClassID: 2618, ObjectID: d.View.RuleOID()})
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
								direct[len(direct)-1].Normal = true
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
					direct[len(direct)-1].Normal = true
					continue
				}
				if d.Inheritance != nil {
					for _, parent := range d.Inheritance.Parents {
						if parent.Relation == target {
							direct = append(direct, table.DomainDependency{Address: a, Target: target, Root: root, OID: a.ObjectID, Label: "table " + name, Relation: d})
							direct[len(direct)-1].Normal = true
							break
						}
					}
				}
				for _, policy := range d.Security.Policies {
					if PolicyDepends(policy, target) {
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}, Target: target, Root: root, OID: policy.OID, Label: "policy " + catalog.DependencyIdentifier(policy.Name) + " on table " + name, Relation: d})
						direct[len(direct)-1].Normal = true
					}
				}
				for i, c := range d.Columns {
					column := a
					column.SubID = d.Attributes[i]
					if typedef.UsageDependsOn(c.Usage, target) {
						direct = append(direct, table.DomainDependency{Address: column, Target: target, Root: root, OID: a.ObjectID, Ordinal: column.SubID, Label: "column " + catalog.DependencyIdentifier(c.Name) + " of " + catalog.RelationDiagnosticKind(d) + " " + name, Relation: d, Column: c.Name})
						direct[len(direct)-1].Normal = true
					}
					if definition, ok := d.ColumnMeta.Defaults[column.SubID]; ok {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								markNormal(ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID})
								if droppedColumns[column] {
									break
								}
								direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID}, Target: target, Root: root, OID: definition.OID, Label: "default value for column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, Relation: d, Automatic: target == column || seen[column] || seen[a]})
								direct[len(direct)-1].Normal = true
								break
							}
						}
					}
					if definition, generated := d.ColumnMeta.Generations[column.SubID]; generated && target != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(c.Usage.Type.ID())}) {
						for _, ref := range definition.Dependencies {
							if ref.Address == target {
								// The recorded edge belongs to the attrdef;
								// INTERNAL ownership reverses to its column.
								markNormal(ids.ObjectAddress{ClassID: 2604, ObjectID: definition.OID})
								markNormal(column)
								if droppedColumns[column] {
									break
								}
								direct = append(direct, table.DomainDependency{Address: column, Target: target, Root: root, OID: a.ObjectID, Ordinal: column.SubID, Label: "column " + catalog.DependencyIdentifier(c.Name) + " of table " + name, Relation: d, Column: c.Name})
								direct[len(direct)-1].Normal = true
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
					if depends {
						localColumn := target.ClassID == a.ClassID && target.ObjectID == a.ObjectID && target.SubID != 0
						address := ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}
						markNormal(address)
						if automatic {
							continue
						}
						direct = append(direct, table.DomainDependency{Address: ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}, Target: target, Root: root, OID: check.OID, Ordinal: int32(check.Ordinal), Label: "constraint " + catalog.DependencyIdentifier(check.Name) + " on table " + name, Relation: d, Automatic: localColumn})
						direct[len(direct)-1].Normal = true
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
						direct[len(direct)-1].Normal = depends
					}
				}
			}
		}
		// An extension member reached through another dependency cannot retire
		// independently of its owning extension. Redirect the incoming edge to
		// that owner; visiting it discovers the complete reciprocal e closure.
		// This is ownership traversal, not diagnostic label substitution.
		for i := range direct {
			edge := &direct[i]
			member := r.RetirementDescriptor(edge.Address)
			owner := catalog.ExtensionMembership(member)
			if owner == (ids.ObjectAddress{}) || seen[owner] || r.Removed(owner) {
				continue
			}
			extension, ok := r.RetirementDescriptor(owner).(*catalog.Extension)
			if !ok || !slices.Contains(extension.Members, edge.Address) {
				return fmt.Errorf("stuntgres: invalid reciprocal extension retirement owner")
			}
			edge.Address, edge.OID = owner, owner.ObjectID
			edge.Relation, edge.Column, edge.Ordinal = nil, "", 0
			edge.Label = stored.DependencyObject(base, owner, path)
		}
		reverse := columnRootOrder || compositeDefaultOrder || domainRootOrder || procedureRootOrder
		sort.Slice(direct, func(i, j int) bool {
			a, b := direct[i].Address, direct[j].Address
			if a.ObjectID != b.ObjectID {
				if reverse {
					return a.ObjectID > b.ObjectID
				}
				return a.ObjectID < b.ObjectID
			}
			if a.ClassID != b.ClassID {
				return a.ClassID < b.ClassID
			}
			return a.SubID < b.SubID
		})
		// Enforcement OIDs do not encode CHECK declaration order. Permute
		// only same-owner CHECK slots on this actual immediate target, leaving
		// every mixed dependent identity and reached-parent choice in place.
		for i := range direct {
			if !columnCheckEdge(direct[i]) {
				continue
			}
			first := i
			for j := i + 1; j < len(direct); j++ {
				if !columnCheckEdge(direct[j]) || direct[j].Relation.Address() != direct[i].Relation.Address() || direct[j].Target != direct[i].Target {
					continue
				}
				if !reverse && direct[j].Ordinal < direct[first].Ordinal || reverse && direct[j].Ordinal > direct[first].Ordinal {
					first = j
				}
			}
			direct[i], direct[first] = direct[first], direct[i]
		}
		for _, edge := range direct {
			edge.Normal = edge.Normal || visits[edge.Address].normal
			if edge.Normal {
				markNormal(edge.Address)
			}
			if seen[edge.Address] {
				if slot, exists := resultSlots[edge.Address]; exists {
					result[slot].Normal = result[slot].Normal || edge.Normal
				}
				if slot, exists := resultSlots[edge.Address]; exists && edge.Automatic && !result[slot].Automatic {
					result[slot].Automatic = true
					result[slot].Target = edge.Target
					result[slot].Root = edge.Root
				}
				// A seeded root expands at its own root-loop turn. Reaching it
				// here still merges flags, but must not steal diagnostic parents
				// or move its consumers ahead of the incumbent root order.
				continue
			}
			owner := edge.Address
			owner.SubID = 0
			if edge.Relation != nil {
				owner = edge.Relation.Address()
			} else if edge.Domain != nil {
				owner = edge.Domain.Address()
			}
			slot := -1
			// Only actual retirement consumes a payload, not discovery of its
			// live whole owner. Owned-index adjacency is also a result carrier
			// after physical removal: consumers and planning still need that
			// exact AUTO edge, as they did before provenance discovery.
			ownedIndex := edge.Automatic && edge.Relation != nil && edge.Relation.SQLIndex != nil && edge.Relation.SQLIndex.Parent.ClassID == target.ClassID && edge.Relation.SQLIndex.Parent.ObjectID == target.ObjectID
			if !r.Removed(owner) || ownedIndex {
				slot = len(result)
				resultSlots[edge.Address] = slot
				result = append(result, edge)
			}
			seen[edge.Address] = true
			if edge.Address.ClassID == ids.TSConfigurationClassID || edge.Address.ClassID == ids.TSParserClassID || edge.Address.ClassID == ids.LanguageClassID {
				if err := visit(edge.Address, root); err != nil {
					return err
				}
			}
			if edge.Address.ClassID == 2620 || edge.Address.ClassID == ids.OperatorClassCatalogID || edge.Address.ClassID == ids.OperatorFamilyClassID || edge.Address.ClassID == ids.TypeClassID || edge.Address.ClassID == ids.RelationClassID || edge.Address.ClassID == ids.ExtensionClassID || edge.Address.ClassID == ids.ProcedureClassID || edge.Address.ClassID == ids.TSTemplateClassID || edge.Address.ClassID == ids.TSDictionaryClassID || edge.Address.ClassID == ids.ForeignWrapperClassID || edge.Address.ClassID == ids.ForeignServerClassID || edge.Address.ClassID == ids.ForeignMappingClassID {
				if err := visit(edge.Address, root); err != nil {
					return err
				}
			}
			if slot >= 0 && (columnRootOrder || compositeDefaultOrder || domainRootOrder || procedureRootOrder) {
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
	// Automatic extension retirement does not waive a clone's partition
	// dependency. A parent trigger or the clone's owning table must retire too.
	// No seeded relation has a partition trigger clone.
	relations := r.Descriptors()
	if newer {
		relations = r.UnseededDescriptors()
	}
	for address, descriptor := range relations {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok || r.Removed(address) || seen[address] {
			continue
		}
		if replacement, ok := r.Replacement(address).(*catalog.Relation); ok {
			relation = replacement
		}
		for _, trigger := range stored.TriggersOf(relation) {
			parent := trigger.Parts().Parent
			if parent.ObjectID != 0 && seen[ids.ObjectAddress{ClassID: 2620, ObjectID: trigger.OID()}] && !seen[parent] {
				return nil, TriggerPartitionDropError(base, relation, trigger, path)
			}
		}
	}
	if columnRootOrder || compositeDefaultOrder || domainRootOrder || procedureRootOrder {
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

func columnCheckEdge(edge table.DomainDependency) bool {
	if edge.Address.ClassID != 2606 || edge.Relation == nil {
		return false
	}
	return slices.ContainsFunc(edge.Relation.Constraints.Check, func(check catalog.CheckConstraint) bool {
		return check.OID == edge.Address.ObjectID
	})
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
			// A rowtype field default carries both the named type and its
			// backing attribute. Preserve the SQL DROP subject even when
			// discovery reaches that attribute before its whole relation.
			if routine, ok := r.RetirementDescriptor(edge.Address).(*catalog.Procedure); ok && routine.SQLDefaults != nil {
				whole := target
				whole.SubID = 0
				if owner, ok := r.RetirementDescriptor(whole).(*catalog.Relation); ok && owner.RelationKind == catalog.CompositeRelation {
					named := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(owner.RowType)}
					if primary == owner.Address() || primary == named {
						target = named
					}
				}
				if typ, ok := r.RetirementDescriptor(target).(*catalog.Type); ok && typ.OwningRelation == primary && r.Removed(primary) {
					// A named composite's backing relation is not its SQL DROP
					// subject. Ordinary table rowtypes do report the table.
					if owner, ok := r.RetirementDescriptor(primary).(*catalog.Relation); ok && owner.RelationKind != catalog.CompositeRelation {
						for _, dependency := range routine.SQLDefaults.Dependencies() {
							address := dependency.Ref.Address
							if address.ClassID == primary.ClassID && address.ObjectID == primary.ObjectID && address.SubID != 0 {
								target = primary
								break
							}
						}
					}
				}
			}
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
	domain, ok := r.RetirementDescriptor(target).(*catalog.Type)
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
		source, ok := r.RetirementDescriptor(address).(*catalog.Relation)
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
	refs, err := stored.QueryDependRefs(r.Context(), base, retirementDefinitions{DefinitionReader: reader, drop: r}, storedView.Query())
	if err != nil {
		return false, err
	}
	return !slices.Contains(refs, target), nil
}

// Retained query recipes are validated against their entering definitions, not
// the live effect projection that has already consumed their providers/columns.
// This borrowed read exists only for dependency classification.
type retirementDefinitions struct {
	catalog.DefinitionReader
	drop DropReader
}

func (r retirementDefinitions) Descriptor(address ids.ObjectAddress) (catalog.Descriptor, bool) {
	descriptor := r.drop.RetirementDescriptor(address)
	return descriptor, descriptor != nil
}
