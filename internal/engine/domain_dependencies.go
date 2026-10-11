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
	column                string
	automatic             bool
}

// PostgreSQL dependency traversal visits a dependent domain's descendants before
// resuming its parent's remaining dependents. Stable attributes are separate
// dependents; a containing table is never a domain-drop target.
func domainDependencyClosure(ctx context.Context, stage *CatalogStage, targets []ObjectAddress) ([]domainDependency, error) {
	domains := make(map[ObjectAddress]bool, len(targets))
	for _, a := range targets {
		domains[a] = true
	}
	for changed := true; changed; {
		changed = false
		for a, d := range stage.base.state.descriptors {
			if t, ok := d.(*typeDescriptor); ok && !domains[a] && (t.typ.IsDomain() && (domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(t.typ.DomainBase().Type.ID())}] || domains[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(t.typ.DomainBase().Collation.ID)}]) || t.typ.IsArray() && domains[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(t.typ.Element().ID())}]) {
				domains[a] = true
				changed = true
			}
		}
	}
	droppedColumns := make(map[ObjectAddress]bool)
	for a, d := range stage.base.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok || r.relationKind != tableRelation && r.relationKind != partitionedRelation && r.relationKind != compositeRelation {
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
	var visit func(ObjectAddress, ObjectAddress) error
	visit = func(target, root ObjectAddress) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if relation, ok := stage.base.state.descriptors[target].(*relationDescriptor); ok && relation.rowType != 0 {
			if err := visit(ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}, root); err != nil {
				return err
			}
			row, ok := stage.base.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
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
		if relation, ok := stage.base.state.descriptors[target].(*relationDescriptor); ok && relation.partition != nil {
			for _, child := range relation.partition.children {
				if _, removed := stage.removals[child]; removed {
					if err := visit(child, root); err != nil {
						return err
					}
				}
			}
		}
		var direct []domainDependency
		for a, descriptor := range stage.base.state.descriptors {
			// Owned indexes are closure roots even if an earlier pass already
			// staged their removal. Their external consumers still need pruning,
			// just like consumers of owned row types and associated arrays.
			if index, ok := descriptor.(*relationDescriptor); ok && index.sqlIndex != nil && index.sqlIndex.parent == target {
				direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true, relation: index})
				continue
			}
			if _, removed := stage.removals[a]; removed {
				continue
			}
			switch d := descriptor.(type) {
			case *typeDescriptor:
				if d.typ.IsDomain() && usageDependsOn(d.typ.DomainBase(), target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "type " + typeDiagnosticNameAt(stage.base, d, stage.dependencyPath)})
				}
				if d.typ.IsArray() && target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(d.typ.Element().ID())}) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, automatic: true})
				}
			case *relationDescriptor:
				if replacement, ok := stage.descriptors[a].(*relationDescriptor); ok {
					d = replacement
				}
				if d.membership != nil && d.membership.parent == target {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, automatic: true})
				}
				if partitionDependsOn(d, target) {
					direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, relation: d, label: "table " + dependencyRelationName(stage.base, d, stage.dependencyPath)})
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
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: "index " + dependencyRelationName(stage.base, d, stage.dependencyPath), relation: d})
					}
					continue
				}
				if d.view != nil {
					if viewDepends(d, target) {
						direct = append(direct, domainDependency{address: a, target: target, root: root, oid: a.ObjectID, label: relationDiagnosticKind(d) + " " + dependencyRelationName(stage.base, d, stage.dependencyPath), relation: d})
					}
					continue
				}
				if d.relationKind != tableRelation && d.relationKind != partitionedRelation && d.relationKind != compositeRelation {
					continue
				}
				name := dependencyRelationName(stage.base, d, stage.dependencyPath)
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
				continue
			}
			seen[edge.address] = true
			result = append(result, edge)
			if edge.address.ClassID == TypeClassID || edge.address.ClassID == RelationClassID {
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

func domainRestrictError(stage *CatalogStage, targets []ObjectAddress, edges []domainDependency, originalRootCount int) error {
	message := "cannot drop desired object(s) because other objects depend on them"
	var primary ObjectAddress
	for i, target := range targets {
		var owner ObjectAddress
		switch descriptor := stage.base.state.descriptors[target].(type) {
		case *typeDescriptor:
			owner = descriptor.owningRelation
		case *relationDescriptor:
			if descriptor.sequence != nil {
				owner = descriptor.sequence.owned
				owner.SubID = 0
			}
		}
		if _, removed := stage.removals[owner]; owner != (ObjectAddress{}) && removed {
			target = owner
		}
		// Explicit roots remain distinct; only automatically retired members
		// share the subject of their removed partition ancestry.
		if i >= originalRootCount {
			for {
				relation, ok := stage.base.state.descriptors[target].(*relationDescriptor)
				if !ok || relation.membership == nil {
					break
				}
				parent := relation.membership.parent
				if _, removed := stage.removals[parent]; !removed {
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
	if primary != (ObjectAddress{}) {
		message = "cannot drop " + dependencyObject(stage.base, primary, stage.dependencyPath) + " because other objects depend on it"
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
			details = append(details, edge.label+" depends on "+dependencyObject(stage.base, target, stage.dependencyPath))
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
	return stageExpressionDependencies(scope.op.Context(), stage, roots, mode, scope, changes, nil)
}

// The same ordered discovery/descriptor core serves SQL, outer Commit and
// drained physical retirement. Only real SQL scopes perform grant acquisition;
// Commit supplies its actual private-root projector without inventing a scope.
func stageExpressionDependencies(ctx context.Context, stage *CatalogStage, roots []ObjectAddress, mode parser.DropMode, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error) error {
	if len(roots) == 0 {
		return nil
	}
	originalRootCount := len(roots)
	// Previously staged whole removals are retirement roots too. Discover their
	// complete dependent closure before classifying any surviving-owner mutation.
	// In particular a schema can remove both a provider and an inheritance parent.
	var retirementRoots []ObjectAddress
	for address, removal := range stage.removals {
		if _, relation := removal.descriptor.(*relationDescriptor); relation && !slices.Contains(roots, address) {
			retirementRoots = append(retirementRoots, address)
		}
	}
	if len(retirementRoots) != 0 {
		sort.Slice(retirementRoots, func(i, j int) bool { return retirementRoots[i].ObjectID < retirementRoots[j].ObjectID })
		roots = append(append([]ObjectAddress(nil), roots...), retirementRoots...)
	}
	if err := lockInheritanceDropClosure(scope, stage, roots); err != nil {
		return err
	}
	edges, err := domainDependencyClosure(ctx, stage, roots)
	if err != nil {
		return err
	}
	visible := false
	for _, edge := range edges {
		visible = visible || !edge.automatic
	}
	if mode == parser.DropRestrict && visible {
		return domainRestrictError(stage, roots, edges, originalRootCount)
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
		if edge.address.ClassID == TypeClassID || edge.column != "" || edge.relation != nil && edge.address == edge.relation.address {
			targets = append(targets, edge.address)
			if edge.address.ClassID == RelationClassID && edge.address.SubID == 0 {
				seen[edge.address] = true
			}
		}
		if edge.relation != nil && edge.address == edge.relation.address && hierarchyRelation(edge.relation) {
			hierarchyTargets = append(hierarchyTargets, edge.address)
		}
	}
	for _, edge := range edges {
		if (edge.column != "" || edge.address.ClassID == 2606) && !seen[edge.relation.address] {
			relations = append(relations, edge.relation)
			seen[edge.relation.address] = true
		}
	}
	for _, edge := range edges {
		if scope != nil && edge.relation != nil && edge.address == edge.relation.address && hierarchyRelation(edge.relation) {
			if err := scope.acquirePartitionDropLocks(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: edge.address}); err != nil {
				return err
			}
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	e := stage.owner
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
			if edge.address.ClassID == PolicyClassID || edge.address == r.address && r.relationKind == tableRelation {
				lockRelations = append(lockRelations, r)
				lockSeen[r.address] = true
			}
		}
		sort.Slice(lockRelations, func(i, j int) bool { return lockRelations[i].address.ObjectID < lockRelations[j].address.ObjectID })
		for _, r := range lockRelations {
			ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: r.address}
			if err := scope.acquireSequence(ref, accessExclusive, true); err != nil {
				return err
			}
		}
	}
	// The complete captured whole-relation closure is retirement authority before
	// any surviving DEFAULT/CHECK or column mutation is classified. Keep explicit
	// root removal policy and the original base/edges for discovery, owned objects,
	// diagnostics and publication fences. Type/collation providers retire later,
	// after surviving column candidates have finished binding against them.
	e.mu.Lock()
	for _, target := range targets[len(roots):] {
		if target.ClassID == RelationClassID && target.SubID == 0 {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: target}, false); err != nil {
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	noticeStart := len(stage.viewCascadeNames)
	if err = stageViewDependents(stage, targets, parser.DropCascade); err != nil {
		return err
	}
	if err := stageForeignKeyDependents(stage, edges); err != nil {
		return err
	}
	for _, old := range relations {
		if _, removed := stage.removals[old.address]; removed {
			continue
		}
		// Whole-relation targets need no standalone shape projection or
		// surviving-participant capability check.
		if slices.Contains(targets, old.address) {
			continue
		}
		ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: old.address}
		next := old
		consumed := false
		for _, edge := range edges {
			if edge.column == "" || edge.relation.address != old.address {
				continue
			}
			if _, removed := stage.removals[old.address]; removed {
				break
			}
			if replacement, ok := stage.descriptors[old.address].(*relationDescriptor); ok {
				next = replacement
			}
			// The closure captured a live stable attribute. A nested cascade
			// may already have consumed it through a different provider path.
			slot := next.indexForAttribute(edge.address.SubID)
			if slot < 0 {
				continue
			}
			if err = inheritanceStageAlterGuard(stage, next); err != nil {
				return err
			}
			next, err = dropColumnCandidate(ctx, stage, next, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: next.columns[slot].Name}, Mode: parser.DropCascade}, scope, changes, project)
			if err != nil {
				return err
			}
			consumed = true
		}
		if !consumed {
			continue
		}
		if _, removed := stage.removals[old.address]; removed {
			continue
		}
		revision, er := stage.cascadeMetadataRevision(old.revision)
		if er != nil {
			return er
		}
		next.revision = revision
		if old.relationKind == compositeRelation {
			if err = stage.Replace(next); err != nil {
				return err
			}
			continue
		}
		if project != nil {
			if err = project(old, next); err != nil {
				return err
			}
			if err = stage.Replace(next); err != nil {
				return err
			}
			continue
		}
		if changes != nil {
			if err = scope.stagePrivateColumnShape(stage, changes, old, next); err != nil {
				return err
			}
			continue
		}
		e.mu.Lock()
		storage := e.stageStorageLocked(stage, old.address)
		storageOwner := stage.base.state.descriptors[old.address].(*relationDescriptor)
		if e.stageCatalogBaseLocked(stage).descriptors[old.address] != storageOwner || storage == nil {
			e.mu.Unlock()
			return CatalogConflict{}
		}
		rows, ends, generation := storage.versions, storage.ends, storage.generation
		e.mu.Unlock()
		nextGeneration := generation + 1
		if generation == ^uint64(0) {
			if !stage.terminalCleanup {
				return fmt.Errorf("stuntgres: storage generation exhausted")
			}
			nextGeneration = generation
		}
		projected, er := projectVersions(ctx, rows, storageOwner, next, nil)
		if er != nil {
			return er
		}
		replacement := &relationStorage{versions: projected, ends: ends, generation: nextGeneration}
		if err = stage.alterRelation(ctx, ref, next, storage, replacement, len(rows), generation, ends); err != nil {
			return err
		}
	}
	e.mu.Lock()
	for _, target := range targets {
		if target.ClassID == TypeClassID || target.ClassID == CollationClassID {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: target}, false); err != nil {
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return err
	}
	stage.viewCascadeNames = stage.viewCascadeNames[:noticeStart]
	for _, edge := range edges {
		if !edge.automatic {
			stage.viewCascadeNames = append(stage.viewCascadeNames, "drop cascades to "+edge.label)
		}
	}
	// Newly discovered hierarchy owners are not the caller's original roots.
	// Stage their AUTO closure after removing them so recursive expression
	// traversal cannot rediscover the same hierarchy during sequence preparation.
	if len(hierarchyTargets) != 0 {
		return stageOwnedExpressionDependencies(ctx, stage, hierarchyTargets, mode, scope, changes, project)
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
				if err := stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: member}, false); err != nil {
					return err
				}
			}
		}
	}
	sort.Slice(contained, func(i, j int) bool { return contained[i].Address().ObjectID < contained[j].Address().ObjectID })
	sort.Slice(stage.domainTargets, func(i, j int) bool { return stage.domainTargets[i].ObjectID < stage.domainTargets[j].ObjectID })
	stage.dependencyPath, _ = e.referencePath(auth)
	for _, d := range contained {
		if err := stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: d.Address()}, false); err != nil {
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
		if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: d.address}, true); err != nil {
			return err
		}
	}
	return nil
}

func usageDependsOn(u sqltype.Usage, target ObjectAddress) bool {
	return target == (ObjectAddress{ClassID: TypeClassID, ObjectID: OID(u.Type.ID())}) || target == (ObjectAddress{ClassID: CollationClassID, ObjectID: OID(u.Collation.ID)})
}
