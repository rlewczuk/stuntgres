package tablecmds

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// stageTriggerMode uses the current statement stage, including ADD-pass FKs.
// Additional descriptor grants are acquired outside Engine.Mu, in OID order.
func stageTriggerMode(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stage *core.CatalogStage, target *catalog.Relation, action parser.AlterTableAction, only bool) (*catalog.Relation, error) {
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	read := e.StageRead(stage)
	candidates, err := table.TriggerModeCandidate(scope.Op.Context(), read, target, action, !only, actor.Superuser)
	if err != nil {
		return nil, err
	}
	for _, next := range candidates {
		if next.Address() == scope.LockedRef.Address {
			continue
		}
		ref := read.Domain().Ref(read.Epoch(), next.Address())
		if err := scope.AcquirePartitionLock(ref, lock.ShareRowExclusive); err != nil {
			return nil, err
		}
	}
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(scope.PendingCatalog())
	for _, next := range candidates {
		if current.Get(next.Address()) != stage.Base.Get(next.Address()) {
			e.Mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
	}
	e.Mu.Unlock()
	for _, next := range candidates {
		before, ok := stage.Base.Relation(next.Address())
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if err := table.BumpAlterRevision(before, next); err != nil {
			return nil, err
		}
		if err := stage.ReplaceDescriptor(next, false, false); err != nil {
			return nil, err
		}
		if next.Address() == target.Address() {
			target = next
		}
	}
	return target, scope.Op.Context().Err()
}

// Called with Engine.Mu held. Only metadata enters the statement journal;
// Finish owns the one atomic publication and all descriptor fences.
func triggerModesLocked(e *core.Engine, scope *core.StatementScope, op *session.Operation, auth core.AuthorizationView, old *catalog.Relation, raw *parser.AlterTable) (core.Result, error) {
	scope.CatalogAuthority = auth.CatalogAuthorityLocked(scope.LockedRef, e.Catalog.State.Get(old.Address()))
	changes := scope.PendingCatalog().Clone()
	stage := authorizedStage(e, nil, auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	next := old
	for _, action := range raw.Actions {
		var err error
		next, err = stageTriggerMode(e, scope, auth, stage, next, action, raw.Only)
		if err != nil {
			return core.Result{}, err
		}
	}
	changed := false
	for address, descriptor := range stage.Descriptors() {
		candidate, ok := descriptor.(*catalog.Relation)
		original, _ := stage.Base.Relation(address)
		if ok && original != nil && table.TriggerModesEqual(candidate, original) {
			if err := stage.ReplaceDescriptor(original, false, false); err != nil {
				return core.Result{}, err
			}
		} else {
			changed = true
		}
	}
	if !changed {
		return core.Result{}, op.Context().Err()
	}
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := scope.ValidateLockedRelation(e); err != nil {
		return core.Result{}, err
	}
	for _, descriptor := range stage.Descriptors() {
		if descriptor == stage.Base.Get(descriptor.Address()) {
			continue
		}
		e.CatalogChangeLocked(changes, descriptor.Address(), descriptor)
	}
	scope.Catalog = changes
	return core.Result{}, op.Context().Err()
}
