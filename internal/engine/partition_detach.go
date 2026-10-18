package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Engine.mu is consumed on every return. The execution scope owns parent AX;
// DEFAULT, candidate and descendant waits use the same ticket without Engine.mu.
func (e *Engine) detachPartitionLocked(scope *StatementScope, op *Operation, auth AuthorizationView, raw *parser.AlterTableDetachPartition, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if scope.lockedRef.Domain() == nil {
		op.session.mu.Lock()
		err := publicationOperationLocked(e, op)
		op.session.mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, raw.Name.Parts[len(raw.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	parent := scope.lockedDescriptor.(*relationDescriptor)
	if parent.partition == nil {
		return fail(partitionDetachKindError(auth.catalog, parent, false))
	}
	if err := e.partitionAttachName(auth, raw.Name, scope.lockedRef); err != nil {
		return fail(err)
	}
	defaultAddress := parent.partition.defaultChild
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), defaultAddress)
		if err := scope.observePartitionRelationLocked(e, ref); err != nil {
			return fail(err)
		}
	}
	e.mu.Unlock()
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.catalog.domain.Ref(auth.catalog.Epoch(), defaultAddress)
		if err := scope.acquirePartitionLock(ref, accessExclusive); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	var candidate ids.ObjectRef
	if err == nil {
		candidate, err = e.resolveRelationReference(auth, raw.Partition, true)
	}
	if err == nil {
		child := auth.catalog.state.descriptors[candidate.Address].(*relationDescriptor)
		if child.relationKind == indexRelation || child.relationKind == compositeRelation {
			err = partitionDetachKindError(auth.catalog, child, true)
		}
		if err == nil {
			err = scope.observePartitionRelationLocked(e, candidate)
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionLock(candidate, accessExclusive); err != nil {
		return Result{}, err
	}
	catalog, child, err := e.partitionAttachRelation(scope, candidate)
	if err != nil {
		return Result{}, err
	}
	if err = partitionDetachMembership(parent, child); err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionDescendants(candidate, accessExclusive); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err = e.statementAuthorizationLocked(op, raw)
	if err == nil {
		err = e.partitionAttachName(auth, raw.Name, scope.lockedRef)
	}
	if err == nil {
		err = e.partitionAttachName(auth, raw.Partition, candidate)
	}
	if err == nil {
		err = auth.Check(AccessRequirement{Object: scope.lockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.validateLockedRelation(e)
	}
	if err == nil && auth.catalog.state.descriptors[parent.address] != parent {
		err = ErrCatalogReplaced
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	catalog = auth.catalog
	child = catalog.state.descriptors[candidate.Address].(*relationDescriptor)
	stage := e.BeginCatalogStage(catalog)
	stage.authorizeLocked(auth, op.session)
	stage.indexOutput = scope.output
	for address := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, catalog.state.descriptors[address])
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = e.stagePartitionDetach(stage, parent, child); err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, cloneCatalogChanges(scope.owner.catalog))
}

func partitionDetachMembership(parent, child *relationDescriptor) error {
	if child.membership == nil || child.membership.parent != parent.address || !slices.Contains(parent.partition.children, child.address) {
		return sqlError("42P01", fmt.Sprintf(`relation "%s" is not a partition of relation "%s"`, child.name, parent.name))
	}
	return nil
}

func partitionDetachKindError(view CatalogRead, d *relationDescriptor, opening bool) error {
	kind := "relations"
	switch d.relationKind {
	case tableRelation:
		kind = "tables"
	case viewRelation:
		kind = "views"
	case materializedViewRelation:
		kind = "materialized views"
	case sequenceRelation:
		kind = "sequences"
	case compositeRelation:
		kind = "composite types"
	case indexRelation:
		kind = "indexes"
		if table, ok := view.state.descriptors[d.sqlIndex.parent].(*relationDescriptor); ok && table.partition != nil {
			kind = "partitioned indexes"
		}
	}
	message := fmt.Sprintf(`ALTER action DETACH PARTITION cannot be performed on relation "%s"`, d.name)
	if opening {
		message = fmt.Sprintf(`cannot open relation "%s"`, d.name)
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: "This operation is not supported for " + kind + "."}
}

// Only descriptors of the immediate edge are replaced. Stores, logical roots,
// rowtype, stable attributes, local definitions and descendant edges are shared.
func (e *Engine) stagePartitionDetach(stage *CatalogStage, parent, child *relationDescriptor) error {
	if err := partitionDetachMembership(parent, child); err != nil {
		return err
	}
	if parent.revision == ids.DefinitionRevision(^uint64(0)) || child.revision == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	nextParent, definition := *parent, *parent.partition
	definition.children = slices.DeleteFunc(slices.Clone(definition.children), func(a ids.ObjectAddress) bool { return a == child.address })
	if definition.defaultChild == child.address {
		definition.defaultChild = ids.ObjectAddress{}
	}
	nextParent.partition = &definition
	nextParent.revision++
	nextChild := *child
	nextChild.membership = nil
	nextChild.revision++
	if len(child.notNullConstraints) != 0 {
		nextChild.notNullConstraints = slices.Clone(child.notNullConstraints)
		for i := range nextChild.notNullConstraints {
			nextChild.notNullConstraints[i].partitionLocal = true
		}
	}
	if len(child.uniqueConstraints) != 0 {
		nextChild.uniqueConstraints = slices.Clone(child.uniqueConstraints)
		for i := range nextChild.uniqueConstraints {
			c := &nextChild.uniqueConstraints[i]
			c.Parent = 0
			c.IsLocal = true
		}
	}
	if len(child.exclusionConstraints) != 0 {
		nextChild.exclusionConstraints = slices.Clone(child.exclusionConstraints)
		for i := range nextChild.exclusionConstraints {
			nextChild.exclusionConstraints[i].Parent = 0
			nextChild.exclusionConstraints[i].IsLocal = true
		}
	}
	if err := stage.Replace(&nextParent); err != nil {
		return err
	}
	if err := stage.Replace(&nextChild); err != nil {
		return err
	}
	for _, descriptor := range stage.base.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != child.address || index.sqlIndex.partitionParent == (ids.ObjectAddress{}) {
			continue
		}
		ancestor, ok := stage.base.state.descriptors[index.sqlIndex.partitionParent].(*relationDescriptor)
		if !ok || ancestor.sqlIndex == nil || ancestor.sqlIndex.parent != parent.address {
			return fmt.Errorf("stuntgres: invalid partition index ancestry")
		}
		if index.revision == ids.DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next, definition := *index, *index.sqlIndex
		definition.partitionParent = ids.ObjectAddress{}
		// The backing contains own identity/kind, not inheritance provenance;
		// retain it while the table constraint alone becomes local.
		next.sqlIndex = &definition
		next.revision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
	}
	// DETACH never validates/promotes an incomplete ONLY ancestor index merely
	// because removing a branch makes its coverage complete.
	return nil
}
