package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The owner-authorized AX grant protects identity; publication changes only the
// immutable descriptor and never evaluates a default or rewrites row storage.
// Every return releases Engine.mu.
func (e *Engine) alterDefaultLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTableDefault, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	if s.Action != parser.SetDefault && s.Action != parser.DropDefault || s.Action == parser.SetDefault && s.Expression == nil || s.Action == parser.DropDefault && s.Expression != nil {
		return fail(fmt.Errorf("stuntgres: invalid default alteration"))
	}
	ref, err := e.resolveReference(auth, s.Name)
	if err == nil && ref != scope.lockedRef {
		err = ids.ErrCatalogReplaced
	}
	if err == nil {
		err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	if err != nil {
		return fail(err)
	}
	old := scope.lockedDescriptor.(*catalog.Relation)
	if catalog.HierarchyRelation(old) && !partitionDefaultReplayAllowed(e, op, auth, s) {
		return fail(sqlError("0A000", "altering partition hierarchies is unsupported"))
	}
	slot, err := table.AlterDefaultColumn(old, s)
	if err != nil {
		return fail(err)
	}
	attr := old.Attributes[slot]
	if catalog.InheritanceParticipant(auth.Catalog, old) {
		return e.alterInheritanceDefaultLocked(scope, op, auth, s, old)
	}
	var definition catalog.ColumnDefault
	if s.Action == parser.SetDefault {
		definition, err = e.cookAlterDefaultLocked(scope, op, auth, old, slot, s.Expression)
		if err != nil {
			return fail(err)
		}
	} else if _, present := old.ColumnMeta.Defaults[attr]; !present {
		op.Session.Mu.Lock()
		err = publicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err == nil {
			scope.completed = true
		}
		e.mu.Unlock()
		return Result{}, err
	}
	next, err := table.AlterDefaultCandidate(old, attr, definition)
	if err != nil {
		return fail(err)
	}
	original := auth.StorageLocked(ref.Address)
	if original == nil && old.Partition == nil {
		return fail(fmt.Errorf("stuntgres: relation storage is missing"))
	}
	var length int
	var generation uint64
	var ends *storage.DeletionRoot
	if original != nil {
		length, generation, ends = len(original.Versions), original.Generation, original.Ends
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if original == nil {
		err = stage.Replace(next)
	} else {
		err = stage.alterRelation(op.Context(), ref, next, original, original, length, generation, ends)
	}
	if err != nil {
		return Result{}, err
	}
	if err := scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	if _, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch}); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}

// cookAlterDefaultLocked cooks SET DEFAULT; the caller holds Engine.mu.
func (e *Engine) cookAlterDefaultLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, old *catalog.Relation, slot int, expression parser.Expr) (catalog.ColumnDefault, error) {
	return table.CookAlterDefault(alterHost{ddlHost{e: e, op: op}, scope}, auth, old, slot, expression)
}
