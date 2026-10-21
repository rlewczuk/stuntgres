package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Engine.mu is consumed on every return. The execution scope owns parent AX;
// DEFAULT, candidate and descendant waits use the same ticket without Engine.mu.
func (e *Engine) detachPartitionLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, raw *parser.AlterTableDetachPartition, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if scope.lockedRef.Domain() == nil {
		op.Session.Mu.Lock()
		err := publicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`relation "%s" does not exist, skipping`, raw.Name.Parts[len(raw.Name.Parts)-1].Text)})
		return Result{}, nil
	}
	parent := scope.lockedDescriptor.(*catalog.Relation)
	if parent.Partition == nil {
		return fail(partition.DetachKindError(auth.Catalog, parent, false))
	}
	if err := e.partitionAttachName(auth, raw.Name, scope.lockedRef); err != nil {
		return fail(err)
	}
	defaultAddress := parent.Partition.DefaultChild
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), defaultAddress)
		if err := scope.observePartitionRelationLocked(e, ref); err != nil {
			return fail(err)
		}
	}
	e.mu.Unlock()
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), defaultAddress)
		if err := scope.acquirePartitionLock(ref, lock.AccessExclusive); err != nil {
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
		child := auth.Catalog.MustRelation(candidate.Address)
		if child.RelationKind == catalog.IndexRelation || child.RelationKind == catalog.CompositeRelation {
			err = partition.DetachKindError(auth.Catalog, child, true)
		}
		if err == nil {
			err = scope.observePartitionRelationLocked(e, candidate)
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionLock(candidate, lock.AccessExclusive); err != nil {
		return Result{}, err
	}
	catalog, child, err := e.partitionAttachRelation(scope, candidate)
	if err != nil {
		return Result{}, err
	}
	if err = partition.DetachMembership(parent, child); err != nil {
		return Result{}, err
	}
	if err = scope.acquirePartitionDescendants(candidate, lock.AccessExclusive); err != nil {
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
		err = auth.Check(access.Requirement{Object: scope.lockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.validateLockedRelation(e)
	}
	if err == nil && auth.Catalog.Get(parent.Address()) != parent {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.mu.Unlock()
		return Result{}, err
	}
	catalog = auth.Catalog
	child = catalog.MustRelation(candidate.Address)
	stage := e.BeginCatalogStage(catalog)
	stage.authorizeLocked(auth, op.Session)
	stage.indexes().Output = scope.output
	for address := range scope.partitionRelations {
		stage.domainBases = append(stage.domainBases, catalog.Get(address))
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = e.stagePartitionDetach(stage, parent, child); err != nil {
		return Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return Result{}, err
	}
	return Result{}, e.stageCatalogOverlay(scope, stage, scope.owner.Catalog.Clone())
}

// Only descriptors of the immediate edge are replaced. Stores, logical roots,
// rowtype, stable attributes, local definitions and descendant edges are shared.
func (e *Engine) stagePartitionDetach(stage *CatalogStage, parent, child *catalog.Relation) error {
	if err := partition.DetachMembership(parent, child); err != nil {
		return err
	}
	if parent.Revision() == ids.DefinitionRevision(^uint64(0)) || child.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	nextParent, err := partition.WithoutChild(parent, child.Address())
	if err != nil {
		return err
	}
	nextChild := *child
	nextChild.Membership = nil
	nextChild.ObjectRevision++
	if len(child.Constraints.NotNull) != 0 {
		nextChild.Constraints.NotNull = slices.Clone(child.Constraints.NotNull)
		for i := range nextChild.Constraints.NotNull {
			nextChild.Constraints.NotNull[i].PartitionLocal = true
		}
	}
	if len(child.Constraints.Unique) != 0 {
		nextChild.Constraints.Unique = slices.Clone(child.Constraints.Unique)
		for i := range nextChild.Constraints.Unique {
			c := &nextChild.Constraints.Unique[i]
			c.Parent = 0
			c.IsLocal = true
		}
	}
	if len(child.Constraints.Exclusion) != 0 {
		nextChild.Constraints.Exclusion = slices.Clone(child.Constraints.Exclusion)
		for i := range nextChild.Constraints.Exclusion {
			nextChild.Constraints.Exclusion[i].Parent = 0
			nextChild.Constraints.Exclusion[i].IsLocal = true
		}
	}
	if err := stage.Replace(nextParent); err != nil {
		return err
	}
	if err := stage.Replace(&nextChild); err != nil {
		return err
	}
	for _, descriptor := range stage.Base.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != child.Address() || index.SQLIndex.PartitionParent == (ids.ObjectAddress{}) {
			continue
		}
		ancestor, ok := stage.Base.Relation(index.SQLIndex.PartitionParent)
		if !ok || ancestor.SQLIndex == nil || ancestor.SQLIndex.Parent != parent.Address() {
			return fmt.Errorf("stuntgres: invalid partition index ancestry")
		}
		if index.Revision() == ids.DefinitionRevision(^uint64(0)) {
			return fmt.Errorf("stuntgres: definition revision exhausted")
		}
		next, definition := *index, *index.SQLIndex
		definition.PartitionParent = ids.ObjectAddress{}
		// The backing contains own identity/kind, not inheritance provenance;
		// retain it while the table constraint alone becomes local.
		next.SQLIndex = &definition
		next.ObjectRevision++
		if err := stage.Replace(&next); err != nil {
			return err
		}
	}
	// DETACH never validates/promotes an incomplete ONLY ancestor index merely
	// because removing a branch makes its coverage complete.
	return nil
}
