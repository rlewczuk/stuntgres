package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Engine.mu is consumed on every return. The execution scope owns parent AX;
// DEFAULT, candidate and descendant waits use the same ticket without Engine.mu.
func detachPartitionLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTableDetachPartition, count int) (core.Result, error) {
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if scope.LockedRef.Domain() == nil {
		return skipMissingRelationLocked(e, scope, op, raw.Name.Parts[len(raw.Name.Parts)-1].Text, true)
	}
	parent := scope.LockedDescriptor.(*catalog.Relation)
	if parent.Partition == nil {
		return fail(partition.DetachKindError(auth.Catalog, parent, false))
	}
	if err := partitionAttachName(e, auth, raw.Name, scope.LockedRef); err != nil {
		return fail(err)
	}
	defaultAddress := parent.Partition.DefaultChild
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), defaultAddress)
		if err := scope.ObservePartitionRelationLocked(e, ref); err != nil {
			return fail(err)
		}
	}
	e.Mu.Unlock()
	if defaultAddress != (ids.ObjectAddress{}) {
		ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), defaultAddress)
		if err := scope.AcquirePartitionLock(ref, lock.AccessExclusive); err != nil {
			return core.Result{}, err
		}
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	var candidate ids.ObjectRef
	if err == nil {
		candidate, err = e.ResolveRelationReference(auth, raw.Partition, true)
	}
	if err == nil {
		child := auth.Catalog.MustRelation(candidate.Address)
		if child.RelationKind == catalog.IndexRelation || child.RelationKind == catalog.CompositeRelation {
			err = partition.DetachKindError(auth.Catalog, child, true)
		}
		if err == nil {
			err = scope.ObservePartitionRelationLocked(e, candidate)
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err = scope.AcquirePartitionLock(candidate, lock.AccessExclusive); err != nil {
		return core.Result{}, err
	}
	catalog, child, err := partitionAttachRelation(e, scope, candidate)
	if err != nil {
		return core.Result{}, err
	}
	if err = partition.DetachMembership(parent, child); err != nil {
		return core.Result{}, err
	}
	if err = scope.AcquirePartitionDescendants(candidate, lock.AccessExclusive); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	auth, err = e.StatementAuthorizationLocked(op, raw)
	if err == nil {
		err = partitionAttachName(e, auth, raw.Name, scope.LockedRef)
	}
	if err == nil {
		err = partitionAttachName(e, auth, raw.Partition, candidate)
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: scope.LockedRef, OwnerOnly: true, OwnerNoun: "table"})
	}
	if err == nil {
		err = scope.ValidateLockedRelation(e)
	}
	if err == nil && auth.Catalog.Get(parent.Address()) != parent {
		err = ids.ErrCatalogReplaced
	}
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	catalog = auth.Catalog
	child = catalog.MustRelation(candidate.Address)
	stage := e.BeginCatalogStage(catalog)
	stage.AuthorizeLocked(auth, op.Session)
	stage.Indexes().Output = scope.Output
	for address := range scope.PartitionRelations {
		stage.DomainBases = append(stage.DomainBases, catalog.Get(address))
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = partition.StageDetach(stage, stage.Base, parent, child); err != nil {
		return core.Result{}, err
	}
	if _, err = e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	return core.Result{}, e.StageCatalogOverlay(scope, stage, scope.Owner.Catalog.Clone())
}
