package tablecmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Metadata-only graph evolution shares the same one-stage atomic publication
// contract as shape ALTER, without running a physical row rewrite.
func renameInheritanceColumnLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, root *catalog.Relation, column, destination string, only bool) (core.Result, error) {
	candidate, err := table.RenameInheritanceColumnPrecheck(auth.Catalog, root, column, destination, only)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	targets, auth, err := acquireInheritanceDefinitions(scope, root, !only)
	if err != nil {
		return core.Result{}, err
	}
	if current := targets[0]; current != root {
		candidate, err = table.RenameColumnCandidate(current, column, destination)
		if err != nil {
			e.Mu.Unlock()
			return core.Result{}, err
		}
		root = current
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.Address()] = true
	}
	for _, old := range targets {
		next, err := table.RenameInheritanceColumn(selected, root, candidate, old, column, destination)
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

func renameInheritanceConstraintLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, raw *parser.AlterTableRenameConstraint, root *catalog.Relation, kind byte) (core.Result, error) {
	name := raw.Constraint.Text
	recurse, err := table.RenameInheritanceConstraintPrecheck(auth.Catalog, root, name, kind, raw.Only)
	if err != nil {
		e.Mu.Unlock()
		return core.Result{}, err
	}
	targets, auth, err := acquireInheritanceDefinitions(scope, root, recurse)
	if err != nil {
		return core.Result{}, err
	}
	root = targets[0]
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	selected := make(map[ids.ObjectAddress]bool, len(targets))
	for _, old := range targets {
		selected[old.Address()] = true
	}
	for _, old := range targets {
		next, err := table.RenameInheritanceConstraint(selected, root, old, name, raw.NewName.Text, kind)
		if err != nil {
			return core.Result{}, err
		}
		if err := stage.Replace(next); err != nil {
			return core.Result{}, err
		}
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

// The caller holds Engine.mu. Success retains it so the caller can create its
// sole stage from the returned authentic authorization/catalog without a gap;
// every error returns unlocked. AX waits always run outside Engine.mu.
func acquireInheritanceDefinitions(scope *core.StatementScope, root *catalog.Relation, recurse bool) ([]*catalog.Relation, core.AuthorizationView, error) {
	e := scope.Engine()
	epoch := scope.LockedRef.Epoch
	captured := map[ids.ObjectAddress]*catalog.Relation{root.Address(): root}
	fail := func(err error) ([]*catalog.Relation, core.AuthorizationView, error) {
		e.Mu.Unlock()
		return nil, core.AuthorizationView{}, err
	}
	for {
		if err := scope.Op.Context().Err(); err != nil {
			return fail(err)
		}
		auth, err := e.AuthorizationLocked(scope.Op)
		if err != nil {
			return fail(err)
		}
		view := auth.Catalog
		if view.Epoch() != epoch {
			return fail(ids.ErrCatalogReplaced)
		}
		for address, old := range captured {
			current, _ := view.Relation(address)
			if !table.InheritanceMarkerPublication(old, current) {
				return fail(ids.ErrCatalogReplaced)
			}
			if current != old {
				captured[address] = current
			}
		}
		root = captured[root.Address()]
		var targets []*catalog.Relation
		if recurse {
			targets = table.InheritanceAlterOrder(view, root)
		} else {
			targets = []*catalog.Relation{root}
		}
		var pending *catalog.Relation
		for _, old := range targets {
			ref := scope.LockedRef
			ref.Address = old.Address()
			if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
				return fail(err)
			}
			if captured[old.Address()] == nil && pending == nil {
				pending = old
			}
		}
		if pending == nil {
			return targets, auth, nil
		}
		captured[pending.Address()] = pending
		ref := scope.LockedRef
		ref.Address = pending.Address()
		e.Mu.Unlock()
		if err := scope.AcquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return nil, core.AuthorizationView{}, err
		}
		e.Mu.Lock()
	}
}
