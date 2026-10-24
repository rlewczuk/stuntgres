package engine

import (
	"errors"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeCreateIndex(scope *StatementScope, s *parser.CreateIndex, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if operationReplayOrZero(scope.op).tableDestination && s.Concurrent.Character != 0 {
		return Result{}, fmt.Errorf("stuntgres: restore cannot execute concurrent index phases")
	}
	scope.owner.Mu.Lock()
	explicit := scope.owner.InBlock()
	scope.owner.Mu.Unlock()
	var nameScratch [8]ids.NameKey
	for {
		var reservedNames []ids.NameKey
		if s.Name.Text == "" && s.Concurrent.Character == 0 {
			e.mu.Lock()
			namespace := scope.lockedRef
			namespace.Address = scope.lockedDescriptor.(*catalog.Relation).Namespace()
			e.mu.Unlock()
			var err error
			reservedNames, err = e.locks.AppendRelationNameClaims(nameScratch[:0], scope.ticket, namespace)
			if err != nil {
				return Result{}, err
			}
		}
		e.mu.Lock()
		fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
		if err := scope.refreshRelationGrantsLocked(e); err != nil {
			return fail(err)
		}
		auth, err := e.statementAuthorizationLocked(scope.op, s)
		if err != nil {
			return fail(err)
		}
		if err = scope.captureIdentityLocked(auth); err != nil {
			return fail(err)
		}
		parent, ok := auth.Catalog.Relation(scope.lockedRef.Address)
		if !ok {
			return fail(ids.ErrCatalogReplaced)
		}
		base := auth.Catalog
		stage := e.BeginCatalogStage(base)
		stage.authorizeLocked(auth, scope.op.Session)
		stage.indexes().Output = scope.output
		failStage := func(err error) (Result, error) {
			e.mu.Unlock()
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		h := ddlHost{e: e, op: scope.op, stage: stage}
		created, err := index.PrepareCreate(h, auth, parent, s, scope.input, reservedNames)
		if err != nil {
			return failStage(err)
		}
		key, ns, name := created.Key, created.Namespace, created.Key.Name
		if created.Skip {
			e.mu.Unlock()
			e.DiscardCatalogStage(stage)
			scope.op.ExistingRelationNotice(name)
			scope.completed = true
			return Result{}, nil
		}
		d := created.Relation
		if s.Concurrent.Character != 0 {
			if err := e.visibilityWriteLocked(scope.txRef); err != nil {
				return failStage(err)
			}
		}
		err = index.StageCreate(h, parent, d, s.Only)
		e.mu.Unlock()
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
		e.DiscardCatalogStage(stage)
		_, conflict := err.(catalog.Conflict)
		if err != nil && !conflict && (s.Name.Text == "" || s.IfNotExists) {
			// Explicit named creation preserves the post-capture catalog race.
			// Existing autocommit/generated recovery remains feature-owned.
			var diagnostic *sqlstate.Error
			conflict = errors.As(err, &diagnostic) && diagnostic.Code == "23505" &&
				diagnostic.Schema == "pg_catalog" && diagnostic.Table == "pg_class" &&
				diagnostic.Constraint == "pg_class_relname_nsp_index"
			if conflict && explicit && s.Name.Text != "" {
				return Result{}, err
			}
		}
		if conflict {
			if contextErr := scope.op.Context().Err(); contextErr != nil {
				return Result{}, contextErr
			}
			if s.Name.Text == "" {
				continue
			}
			if s.IfNotExists {
				e.mu.Lock()
				currentAuth, authErr := e.statementAuthorizationLocked(scope.op, s)
				current := e.privateCatalogLocked(scope.owner.Catalog)
				_, exists := current.LookupName(key)
				same := current.Epoch() == base.Epoch() && current.Get(parent.Address()) != nil
				if authErr == nil && same {
					authErr = currentAuth.Check(access.Requirement{Object: scope.lockedRef, OwnerOnly: true})
				}
				if authErr == nil && same {
					authErr = currentAuth.CheckCreate(ns)
				}
				e.mu.Unlock()
				if authErr != nil {
					return Result{}, authErr
				}
				if same && exists {
					scope.op.ExistingRelationNotice(name)
					scope.completed = true
					return Result{}, nil
				}
			}
			return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if err == nil && s.Concurrent.Character != 0 {
			e.mu.Lock()
			e.visibility.ResetPhaseLocked(scope.txRef)
			e.mu.Unlock()
			return e.executeConcurrentIndex(scope, s, d)
		}
		if err == nil {
			scope.completed = true
		}
		return Result{}, err
	}
}
