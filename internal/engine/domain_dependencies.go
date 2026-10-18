package engine

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type domainDependency struct {
	address, target, root ObjectAddress
	oid                   OID
	ordinal               int32
	label                 string
	relation              *relationDescriptor
	domain                *typeDescriptor
	column                string
	automatic             bool
}

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func domainDependencyClosure(ctx context.Context, stage *CatalogStage, targets []ObjectAddress) ([]domainDependency, error) {
	return stageDropProvider(ctx, stage, nil, nil, nil).dependencyClosure(targets)
}

func (provider *dropProvider) dependencyClosure(targets []ObjectAddress) ([]domainDependency, error) {
	ctx := provider.ctx
	domains := make(map[ObjectAddress]bool, len(targets))
	for _, a := range targets {
		domains[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a, d := range provider.inputDescriptors() {
			if t, ok := d.(*typeDescriptor); ok && !domains[a] && (t.typ.IsDomain() && (domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(t.typ.DomainBase().Type.ID())}] || domains[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(t.typ.DomainBase().Collation.ID)}]) || t.typ.IsArray() && domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(t.typ.Element().ID())}]) {
				domains[a] = true
				changed = true
			}
		}
	}
	droppedColumns := make(map[ObjectAddress]bool)
	for a, d := range provider.inputDescriptors() {
		r, ok := d.(*relationDescriptor)
		if !ok || r.relationKind != tableRelation && r.relationKind != partitionedRelation && r.relationKind != compositeRelation && r.relationKind != foreignRelation {
			continue
		}
		for i, c := range r.columns {
			if domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(c.Usage.Type.ID())}] || domains[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(c.Usage.Collation.ID)}] {
				column := a
				column.SubID = r.attributes[i]
				droppedColumns[column] = true
			}
		}
	}
	seen := make(map[ObjectAddress]bool)
	for _, a := range targets {
		seen[a] = true
	}
	var result []domainDependency
	resultSlots := make(map[ObjectAddress]int)
	mergedDescriptors := provider.descriptorCount() != len(provider.base.state.descriptors)
	var visit func(ObjectAddress, ObjectAddress) error
	visit = func(target, root ObjectAddress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relation, ok := provider.retirementDescriptor(target).(*relationDescriptor); ok && relation.rowType != 0 {
			if err := visit(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}, root); err != nil {
				return err
			}
			row, ok := provider.retirementDescriptor(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}).(*typeDescriptor)
			if !ok || row.owningRelation != relation.address {
				return fmt.Errorf("stuntgres: missing row type dependency owner")
			}
			// Removal already stages the owned array, so the descriptor scan
			// below cannot discover it as an automatic dependent.
			if row.array != 0 {
				if err := visit(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}, root); err != nil {
					return err
				}
			}
		}
		if relation, ok := provider.retirementDescriptor(target).(*relationDescriptor); ok && relation.partition != nil {
			for _, child := range relation.partition.children {
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
			if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil && index.sqlIndex.parent == target {
				direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true, relation: index})
				continue
			}
			if provider.removed(a) {
				continue
			}
			if target.ClassID == NamespaceClassID && descriptor.Namespace() == target && ownershipRoot(provider.base, descriptor) {
				edge := domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)}
				edge.relation, _ = descriptor.(*relationDescriptor)
				direct = append(direct, edge)
				continue
			}
			switch d := descriptor.(type) {
			case *operatorDescriptor:
				for _, dependency := range operatorDependencyAddresses(provider.base, d) {
					if dependency == target {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
						break
					}
				}
			case *foreignDescriptor:
				for _, dependency := range foreignDependencyAddresses(d) {
					if dependency == (ObjectAddress{}) {
						continue
					}
					if dependency == target {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
						break
					}
				}
			case *extensionDescriptor:
				if d.targetNamespace == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path)})
				}
			case *procedureDescriptor:
				if d.extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *tsDictionaryDescriptor:
				if d.extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *tsTemplateDescriptor:
				if d.extension == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *typeDescriptor:
				if replacement, ok := provider.replacement(a).(*typeDescriptor); ok {
					d = replacement
				}
				if !domains[a] {
					for _, check := range d.domainChecks {
						for _, dep := range check.dependencies {
							if dep.ref.Address == target {
								direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2606, ObjectID: check.OID}, target: target, root: root, oid: check.OID, label: "constraint " + dependencyIdentifier(check.Name), domain: d})
								break
							}
						}
					}
				}
				if d.typ.IsDomain() && usageDependsOn(d.typ.DomainBase(), target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "type " + typeDiagnosticNameAt(provider.base, d, provider.path)})
				}
				if d.typ.IsArray() && target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.Element().ID())}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *relationDescriptor:
				if replacement, ok := provider.replacement(a).(*relationDescriptor); ok {
					d = replacement
				}
				if d.foreign != nil && d.foreign.server == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: dependencyObject(provider.base, a, provider.path), relation: d})
				}
				if d.relationKind == compositeRelation && target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.rowType)}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true})
					continue
				}
				if materializedExtensionDepends(d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true, label: "materialized view " + dependencyRelationName(provider.base, d, provider.path)})
				}
				if d.membership != nil && d.membership.parent == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true})
				}
				if partitionDependsOn(d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, label: "table " + dependencyRelationName(provider.base, d, provider.path)})
					continue
				}
				if d.sqlIndex != nil {
					depends, automatic := false, false
					for _, key := range d.sqlIndex.keys {
						depends = depends || target == (ObjectAddress{ClassID: CollationClassID, ObjectID: OID(key.collation)})
						if key.derived != nil {
							for _, dep := range key.derived.dependencies {
								depends = depends || dep.Address == target
								automatic = automatic || droppedColumns[dep.Address]
							}
						}
					}
					if predicate := d.sqlIndex.predicate; predicate != nil {
						for _, dep := range predicate.dependencies {
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
				for _, rule := range d.rules {
					if ruleDepends(provider.base, rule, target) {
						if name == "" {
							name = dependencyRelationName(provider.base, d, provider.path)
						}
						direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2618, ObjectID: rule.OID}, target: target, root: root, oid: rule.OID, label: "rule " + dependencyIdentifier(rule.Name) + " on " + relationDiagnosticKind(d) + " " + name, relation: d})
					}
				}
				if d.view != nil {
					if viewDepends(d, target) {
						if name == "" {
							name = dependencyRelationName(provider.base, d, provider.path)
						}
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: relationDiagnosticKind(d) + " " + name, relation: d})
					}
					continue
				}
				if d.relationKind != tableRelation && d.relationKind != partitionedRelation && d.relationKind != compositeRelation && d.relationKind != foreignRelation {
					continue
				}
				if name == "" {
					name = dependencyRelationName(provider.base, d, provider.path)
				}
				if d.ofType == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "table " + name, relation: d})
					continue
				}
				if d.inheritance != nil {
					for _, parent := range d.inheritance.parents {
						if parent == target {
							direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "table " + name, relation: d})
							break
						}
					}
				}
				for _, policy := range d.policies {
					if policyDepends(policy, target) {
						direct = append(direct, domainDependency{address: ObjectAddress{ClassID: PolicyClassID, ObjectID: policy.OID}, target: target, root: root, oid: policy.OID, label: "policy " + dependencyIdentifier(policy.Name) + " on table " + name, relation: d})
					}
				}
				for i, c := range d.columns {
					column := a
					column.SubID = d.attributes[i]
					if usageDependsOn(c.Usage, target) {
						direct = append(direct, domainDependency{address: column, target: target, root: root, oid: a.ObjectID, ordinal: column.SubID, label: "column " + dependencyIdentifier(c.Name) + " of " + relationDiagnosticKind(d) + " " + name, relation: d, column: c.Name})
					}
					if droppedColumns[column] {
						continue
					}
					if definition, ok := d.defaults[column.SubID]; ok {
						for _, ref := range definition.dependencies {
							if ref.Address == target {
								direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2604, ObjectID: definition.OID}, target: target, root: root, oid: definition.OID, label: "default value for column " + dependencyIdentifier(c.Name) + " of table " + name, relation: d})
								break
							}
						}
					}
					if definition, generated := d.generations[column.SubID]; generated && target != (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(c.Usage.Type.ID())}) {
						for _, ref := range definition.dependencies {
							if ref.Address == target {
								direct = append(direct, domainDependency{address: column, target: target, root: root, oid: a.ObjectID, ordinal: column.SubID, label: "column " + dependencyIdentifier(c.Name) + " of table " + name, relation: d, column: c.Name})
								break
							}
						}
					}
				}
				for _, check := range d.checkConstraints {
					automatic := false
					depends := false
					for _, dep := range check.dependencies {
						automatic = automatic || droppedColumns[dep.ref.Address]
						depends = depends || dep.ref.Address == target
					}
					if depends && !automatic {
						orderOID, ordinal := check.OID, int32(0)
						if target.ClassID == RelationClassID {
							orderOID, ordinal = a.ObjectID, int32(check.ordinal)
						}
						direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2606, ObjectID: check.OID}, target: target, root: root, oid: orderOID, ordinal: ordinal, label: "constraint " + dependencyIdentifier(check.Name) + " on table " + name, relation: d})
					}
				}
				for _, f := range d.foreignKeys {
					depends := target == f.Index || target == f.Target
					if target.ClassID == f.Target.ClassID && target.ObjectID == f.Target.ObjectID && target.SubID != 0 {
						depends = slices.Contains(f.Referenced, target.SubID)
					}
					automatic := target.ClassID == d.address.ClassID && target.ObjectID == d.address.ObjectID && (target.SubID == 0 || slices.Contains(f.Attributes, target.SubID))
					if depends || automatic {
						direct = append(direct, domainDependency{address: ObjectAddress{ClassID: 2606, ObjectID: f.OID}, target: target, root: root, oid: f.OID, label: "constraint " + dependencyIdentifier(f.Name) + " on table " + name, relation: d, automatic: automatic})
					}
				}
			}
		}
		sort.Slice(direct, func(i, j int) bool {
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
			resultSlots[edge.address] = len(result)
			result = append(result, edge)
			if edge.address.ClassID == TypeClassID || edge.address.ClassID == RelationClassID || edge.address.ClassID == ExtensionClassID || edge.address.ClassID == ProcedureClassID || edge.address.ClassID == TSTemplateClassID || edge.address.ClassID == TSDictionaryClassID || edge.address.ClassID == ForeignWrapperClassID || edge.address.ClassID == ForeignServerClassID || edge.address.ClassID == ForeignMappingClassID {
				if err := visit(edge.address, root); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, root := range targets {
		if err := visit(root, root); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func domainRestrictError(stage *CatalogStage, targets []ObjectAddress, edges []domainDependency, originalRootCount int, subject ObjectAddress) error {
	return stageDropProvider(context.Background(), stage, nil, nil, nil).restrictError(targets, edges, originalRootCount, subject)
}

func (provider *dropProvider) restrictError(targets []ObjectAddress, edges []domainDependency, originalRootCount int, subject ObjectAddress) error {
	message := "cannot drop desired object(s) because other objects depend on them"
	primary := subject
	if primary == (ObjectAddress{}) {
		for i, target := range targets {
			var owner ObjectAddress
			switch descriptor := provider.retirementDescriptor(target).(type) {
			case *typeDescriptor:
				owner = descriptor.owningRelation
			case *relationDescriptor:
				if descriptor.sequence != nil {
					owner = descriptor.sequence.owned
					owner.SubID = 0
				}
			}
			if owner != (ObjectAddress{}) && provider.removed(owner) {
				target = owner
			}
			// Explicit roots remain distinct; only automatically retired members
			// share the subject of their removed partition ancestry.
			if i >= originalRootCount {
				for {
					relation, ok := provider.retirementDescriptor(target).(*relationDescriptor)
					if !ok || relation.membership == nil {
						break
					}
					parent := relation.membership.parent
					if !provider.removed(parent) {
						break
					}
					target = parent
				}
			}
			if primary == (ObjectAddress{}) {
				primary = target
			} else if primary != target {
				primary = ObjectAddress{}
				break
			}
		}
	}
	if primary != (ObjectAddress{}) {
		message = "cannot drop " + dependencyObject(provider.base, primary, provider.path) + " because other objects depend on it"
	}
	details := make([]string, 0, len(edges))
	for _, edge := range edges {
		if !edge.automatic {
			target := edge.target
			if edge.address.ClassID == 2606 && edge.relation != nil {
				for _, f := range edge.relation.foreignKeys {
					if f.OID == edge.address.ObjectID && edge.root.ClassID == f.Target.ClassID && edge.root.ObjectID == f.Target.ObjectID {
						target = edge.root
						break
					}
				}
			}
			object := dependencyObject(provider.base, target, provider.path)
			if index, ok := provider.retirementDescriptor(target).(*relationDescriptor); ok && index.sqlIndex != nil {
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
func (scope *StatementScope) stageExpressionDependents(stage *CatalogStage, roots []ObjectAddress, mode parser.DropMode, changes *catalogChanges) error {
	return stageExpressionDependencies(scope.op.Context(), stage, roots, mode, scope, changes, nil, ObjectAddress{})
}

// The same ordered discovery/descriptor core serves SQL, outer Commit and
// drained physical retirement. Only real SQL scopes perform grant acquisition;
// Commit supplies its actual private-root projector without inventing a scope.
// A nonzero subject attributes only the RESTRICT message to one SQL DROP;
// retirement roots, dependency discovery, locks and mutations stay unchanged.
func stageExpressionDependencies(ctx context.Context, stage *CatalogStage, roots []ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error, subject ObjectAddress) error {
	return stageDropProvider(ctx, stage, scope, changes, project).expressionDependencies(roots, mode, subject)
}

func (provider *dropProvider) expressionDependencies(roots []ObjectAddress, mode parser.DropMode, subject ObjectAddress) error {
	scope := provider.scope
	if len(roots) == 0 {
		return nil
	}
	originalRootCount := len(roots)
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	var retirementRoots []ObjectAddress
	for address, descriptor := range provider.removalDescriptors() {
		if _, relation := descriptor.(*relationDescriptor); relation && !slices.Contains(roots, address) {
			retirementRoots = append(retirementRoots, address)
		}
	}
	if len(retirementRoots) != 0 {
		sort.Slice(retirementRoots, func(i, j int) bool { return retirementRoots[i].ObjectID < retirementRoots[j].ObjectID })
		roots = append(append([]ObjectAddress(nil), roots...), retirementRoots...)
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
	targets := append([]ObjectAddress(nil), roots...)
	var relations []*relationDescriptor
	var hierarchyTargets []ObjectAddress
	seen := make(map[ObjectAddress]bool)
	for _, target := range targets {
		if target.ClassID == RelationClassID && target.SubID == 0 {
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
		if foreignCatalogClass(edge.address.ClassID) || edge.address.ClassID == OperatorClassID || edge.address.ClassID == TypeClassID || edge.address.ClassID == ExtensionClassID || edge.address.ClassID == ProcedureClassID || edge.address.ClassID == TSTemplateClassID || edge.address.ClassID == TSDictionaryClassID || edge.column != "" || edge.relation != nil && edge.address == edge.relation.address {
			targets = append(targets, edge.address)
			if edge.address.ClassID == RelationClassID && edge.address.SubID == 0 {
				seen[edge.address] = true
			}
		}
		if edge.relation != nil && edge.address == edge.relation.address && hierarchyRelation(edge.relation) {
			hierarchyTargets = append(hierarchyTargets, edge.address)
		}
	}
	// The final closure can discover SQL/MED providers below a schema,
	// registered procedure or wrapper root. Fence their incoming identities
	// together with the existing extension retirement observation.
	for _, target := range targets {
		observe := target.ClassID == ExtensionClassID || target.ClassID == ForeignWrapperClassID || target.ClassID == ForeignServerClassID
		if target.ClassID == ProcedureClassID {
			procedure, ok := provider.retirementDescriptor(target).(*procedureDescriptor)
			observe = ok && procedure.foreign != nil
		}
		if observe {
			provider.observeIdentityDependencies(targets)
			break
		}
	}
	for _, edge := range edges {
		if edge.relation != nil && (edge.column != "" || edge.address.ClassID == 2606) && !seen[edge.relation.address] {
			relations = append(relations, edge.relation)
			seen[edge.relation.address] = true
		}
	}
	for _, edge := range edges {
		if scope != nil && edge.relation != nil && edge.address == edge.relation.address && hierarchyRelation(edge.relation) {
			if err := scope.acquirePartitionDropLocks(ObjectRef{owner: provider.owner.domain, Epoch: provider.base.Epoch(), Address: edge.address}); err != nil {
				return err
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	e := provider.owner
	if scope != nil {
		// Lock owners are not deletion/projection targets. A policy edge
		// captures its surviving owner's identity, even without a column edge.
		lockRelations := make([]*relationDescriptor, 0, len(relations))
		lockSeen := make(map[ObjectAddress]bool)
		for _, r := range relations {
			lockRelations = append(lockRelations, r)
			lockSeen[r.address] = true
		}
		for _, edge := range edges {
			r := edge.relation
			if r == nil || lockSeen[r.address] {
				continue
			}
			if edge.address.ClassID == PolicyClassID || edge.address == r.address && (r.relationKind == tableRelation || r.relationKind == foreignRelation) {
				lockRelations = append(lockRelations, r)
				lockSeen[r.address] = true
			}
		}
		sort.Slice(lockRelations, func(i, j int) bool { return lockRelations[i].address.ObjectID < lockRelations[j].address.ObjectID })
		for _, r := range lockRelations {
			ref := ObjectRef{owner: e.domain, Epoch: provider.base.Epoch(), Address: r.address}
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
		if target.ClassID == RelationClassID && target.SubID == 0 {
			if err = provider.remove(ObjectRef{owner: e.domain, Epoch: provider.base.Epoch(), Address: target}); err != nil {
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
		if provider.removed(old.address) {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, old.address) {
			continue
		}
		ref := ObjectRef{owner: e.domain, Epoch: provider.base.Epoch(), Address: old.address}
		next := old
		consumed := false
		for _, edge := range edges {
			if edge.column == "" || edge.relation.address != old.address {
				continue
			}
			if provider.removed(old.address) {
				break
			}
			if replacement, ok := provider.replacement(old.address).(*relationDescriptor); ok {
				next = replacement
			}
			// The closure captured a live stable attribute. A nested cascade
			// may already have consumed it through a different provider path.
			slot := next.indexForAttribute(edge.address.SubID)
			if slot < 0 {
				continue
			}
			if err = provider.inheritanceAlterGuard(next); err != nil {
				return err
			}
			next, err = ordinaryColumnDrop(provider, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: next.columns[slot].Name}, Mode: parser.DropCascade})
			if err != nil {
				return err
			}
			consumed = true
		}
		if !consumed {
			continue
		}
		if provider.removed(old.address) {
			continue
		}
		revision, er := provider.revision(old.revision)
		if er != nil {
			return er
		}
		next.revision = revision
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
		if foreignCatalogClass(target.ClassID) || target.ClassID == OperatorClassID || target.ClassID == TypeClassID || target.ClassID == CollationClassID || target.ClassID == ConversionClassID || target.ClassID == ExtensionClassID || target.ClassID == ProcedureClassID || target.ClassID == TSTemplateClassID || target.ClassID == TSDictionaryClassID {
			if err = provider.remove(ObjectRef{owner: e.domain, Epoch: provider.base.Epoch(), Address: target}); err != nil {
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

func (e *Engine) stageSchemaDomainsLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, schemas []*namespaceDescriptor, relations []*relationDescriptor, domains []Descriptor, cascade bool) error {
	var contained []Descriptor
	for _, r := range relations {
		contained = append(contained, r)
	}
	for _, d := range domains {
		contained = append(contained, d)
		stage.domainTargets = append(stage.domainTargets, d.Address())
		if extension, ok := d.(*extensionDescriptor); ok {
			for _, member := range extension.members {
				stage.domainTargets = append(stage.domainTargets, member)
				if err := stage.removeObjectLocked(ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: member}, false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.domainTargets, func(i, j int) bool { return stage.domainTargets[i].ObjectID < stage.domainTargets[j].ObjectID })
	stage.dependencyPath, _ = e.referencePath(auth)
	for _, d := range contained {
		if err := stage.removeObjectLocked(ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: d.Address()}, false); err != nil {
			return err
		}
	}
	edges, err := domainDependencyClosure(op.Context(), stage, stage.domainTargets)
	if err != nil {
		return err
	}
	var details []string
	for _, d := range contained {
		label := dependencyObject(stage.base, d.Address(), stage.dependencyPath)
		namespaceAddress := d.Namespace()
		if extension, ok := d.(*extensionDescriptor); ok {
			namespaceAddress = extension.targetNamespace
		}
		namespace := stage.base.state.descriptors[namespaceAddress].Name()
		details = append(details, label+" depends on schema "+dependencyIdentifier(namespace))
		stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+label)
		for _, edge := range edges {
			root := edge.root == d.Address()
			if extension, ok := d.(*extensionDescriptor); ok {
				root = root || slices.Contains(extension.members[:], edge.root)
			}
			if root && !edge.automatic {
				details = append(details, edge.label+" depends on "+dependencyObject(stage.base, edge.target, stage.dependencyPath))
			}
		}
	}
	if !cascade {
		message := "cannot drop desired object(s) because other objects depend on them"
		if len(schemas) == 1 {
			message = "cannot drop schema " + dependencyIdentifier(schemas[0].name) + " because other objects depend on it"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	}
	for _, d := range schemas {
		if err = stage.removeObjectLocked(ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: d.address}, true); err != nil {
			return err
		}
	}
	return nil
}

func usageDependsOn(u sqltype.Usage, target ObjectAddress) bool {
	return target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}) || target == (ObjectAddress{ClassID: CollationClassID, ObjectID: OID(u.Collation.ID)})
}

func (stage *CatalogStage) removeDomainCheck(d *typeDescriptor, oid OID) error {
	return stageDropProvider(nil, stage, nil, nil, nil).removeDomainCheck(d, oid)
}

func (provider *dropProvider) removeDomainCheck(d *typeDescriptor, oid OID) error {
	if replacement, ok := provider.replacement(d.Address()).(*typeDescriptor); ok {
		d = replacement
	}
	next := *d
	next.domainChecks = make([]domainCheckConstraint, 0, len(d.domainChecks))
	for _, check := range d.domainChecks {
		if check.OID != oid {
			next.domainChecks = append(next.domainChecks, check)
		}
	}
	var err error
	next.typ, err = sqltype.Domain(d.typ.ID(), d.Name(), d.typ.SQLName(), d.typ.DomainBase(), len(next.domainChecks) != 0)
	if err != nil {
		return err
	}
	next.revision, err = provider.revision(d.revision)
	if err != nil {
		return err
	}
	return provider.replace(&next)
}
