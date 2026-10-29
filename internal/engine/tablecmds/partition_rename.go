package tablecmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// Entry holds Engine.Mu and the addressed target's owner-authorized AX grant.
// All returns release Engine.Mu. The complete AX closure precedes descendant
// authority/column checks; no candidate reaches the catalog until all succeed.
func renamePartitionColumnLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, root *catalog.Relation, column, destination string, only bool) (core.Result, error) {
	snapshot := scope.PendingDetachSnapshot()
	selectChildren := func(parent *catalog.Relation) []ids.ObjectAddress {
		return partition.EffectiveChildren(auth.Catalog, parent, snapshot)
	}
	if only {
		if err := table.PartitionRenameOnly(selectChildren(root), column); err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
	}
	targets, auth, err := acquirePartitionRenameDefinitions(scope, auth, root, !only, selectChildren)
	if err != nil {
		return core.Result{}, err
	}
	if err := auth.Check(access.Requirement{Object: scope.LockedRef, OwnerOnly: true}); err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	// Provider callbacks may replace the catalog; publication retains the epoch fence.
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	defer e.DiscardCatalogStage(stage)
	// Descendants in breadth-first order, then the addressed root last.
	for i := range targets {
		old := targets[(i+1)%len(targets)]
		ref := scope.LockedRef
		ref.Address = old.Address()
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return core.Result{}, err
		}
		next, err := table.RenamePartitionColumn(old, column, destination, old.Address() == root.Address())
		if err != nil {
			return core.Result{}, err
		}
		if err := stage.Replace(next); err != nil {
			return core.Result{}, err
		}
	}
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return core.Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
	}
	if err == nil {
		scope.CompleteUtility()
	}
	return core.Result{}, err
}

// Capture snapshot-visible membership once under the root AX grant. Wait outside Mu,
// using the statement/transaction ticket; refresh authentic authorization and
// every captured descriptor after each wait without broadening the closure.
// Success retains Mu for stage creation; errors return unlocked.
func acquirePartitionRenameDefinitions(scope *core.StatementScope, auth core.AuthorizationView, root *catalog.Relation, recurse bool, selectChildren func(*catalog.Relation) []ids.ObjectAddress) ([]*catalog.Relation, core.AuthorizationView, error) {
	e := scope.Engine()
	targets := []*catalog.Relation{root}
	if recurse {
		targets = table.PartitionRenameOrder(auth.Catalog, root, selectChildren)
	}
	fail := func(err error) ([]*catalog.Relation, core.AuthorizationView, error) {
		e.Mu.Unlock()
		return nil, core.AuthorizationView{}, err
	}
	for i := range targets {
		if i != 0 {
			ref := scope.LockedRef
			ref.Address = targets[i].Address()
			e.Mu.Unlock()
			if err := scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
				return nil, core.AuthorizationView{}, err
			}
			e.Mu.Lock()
		}
		if err := scope.Op.Context().Err(); err != nil {
			return fail(err)
		}
		var err error
		auth, err = e.AuthorizationLocked(scope.Op)
		if err != nil {
			return fail(err)
		}
		if auth.Catalog.Epoch() != scope.LockedRef.Epoch {
			return fail(ids.ErrCatalogReplaced)
		}
		for j, old := range targets {
			current, _ := auth.Catalog.Relation(old.Address())
			if !table.InheritanceMarkerPublication(old, current) {
				return fail(ids.ErrCatalogReplaced)
			}
			targets[j] = current
		}
	}
	return targets, auth, nil
}
