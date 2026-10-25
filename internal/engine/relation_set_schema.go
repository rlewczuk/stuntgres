package engine

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Engine.mu is held on entry and released on every return. Dependency waits
// and provider callbacks run without it; one stage publishes the entire closure.
func (e *Engine) alterSchemaLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.AlterTableSetSchema, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	ref, err := e.ResolveRelationReference(auth, s.Name, true)
	if err == nil && ref != scope.LockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	if s.IfExists && table.RenameMissing(err) {
		return e.skipMissingRelationLocked(scope, op, s.Name.Parts[len(s.Name.Parts)-1].Text, false)
	}
	if err != nil {
		return fail(err)
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	old := scope.LockedDescriptor.(*catalog.Relation)
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.ViewRelation && old.RelationKind != catalog.MaterializedViewRelation && old.RelationKind != catalog.ForeignRelation {
		return fail(SQLError("0A000", "ALTER TABLE SET SCHEMA supports ordinary tables only"))
	}
	destination, err := e.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.Name()}}})
	if err != nil {
		return fail(err)
	}
	if err = access.CheckPersistentCreationNamespace(auth.Catalog, destination.Address); err != nil {
		return fail(err)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
	}
	if auth.Catalog.MustNamespace(old.Namespace()).Temporary != (ids.SessionToken{}) {
		return fail(SQLError("0A000", "cannot move objects into or out of temporary schemas"))
	}
	if destination.Address == old.Namespace() {
		op.Session.Mu.Lock()
		err = PublicationOperationLocked(e, op)
		op.Session.Mu.Unlock()
		if err == nil {
			err = op.Context().Err()
		}
		if err != nil {
			return fail(err)
		}
		scope.Completed = true
		e.Mu.Unlock()
		return Result{}, nil
	}
	owned := table.SetSchemaOwned(auth.Catalog, old)
	e.Mu.Unlock()
	for _, r := range owned {
		if r.SQLIndex != nil {
			continue
		}
		if err = scope.AcquireSequence(e.Catalog.Domain.Ref(ref.Epoch, r.Address()), lock.AccessExclusive, false); err != nil {
			return Result{}, err
		}
	}
	// Dependency admission does not reserve replacement; release any existing
	// admission before validation, whose provider may replace the catalog.
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	e.Mu.Lock()
	auth, err = e.StatementAuthorizationLocked(op, s)
	if err != nil {
		return fail(err)
	}
	current, err := e.ResolveRelationReference(auth, s.Name, true)
	if err != nil {
		return fail(err)
	}
	if current != ref || auth.Catalog.Get(ref.Address) != old {
		return fail(ids.ErrCatalogReplaced)
	}
	if err = auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	liveDestination, err := e.CreationNamespace(auth, parser.ObjectName{Parts: []parser.Identifier{s.Schema, {Text: old.Name()}}})
	if err != nil {
		return fail(err)
	}
	if liveDestination != destination {
		return fail(ids.ErrCatalogReplaced)
	}
	if err = auth.CheckCreate(destination); err != nil {
		return fail(err)
	}
	group, err := table.SetSchemaGroup(auth.Catalog, old, owned)
	if err != nil {
		return fail(err)
	}
	next, err := table.SetSchemaCandidates(auth.Catalog, group, destination.Address, s.Schema.Text)
	if err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, op.Session)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	for i := range next {
		if err = stage.Replace(&next[i]); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
	}
	if _, conflict := err.(catalog.Conflict); conflict {
		e.Mu.Lock()
		current := e.StageCatalogBaseLocked(stage)
		if current.Epoch() == ref.Epoch {
			if collision := table.SetSchemaConflict(e.Catalog.Read(current), group, destination.Address, s.Schema.Text); collision != nil {
				err = collision
			}
		}
		e.Mu.Unlock()
	}
	if err != nil {
		return Result{}, err
	}
	scope.Completed = true
	return Result{}, nil
}
