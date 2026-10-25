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

func executeCreateIndex(e *Engine, scope *StatementScope, s *parser.CreateIndex, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if OperationReplayOrZero(scope.Op).TableDestination && s.Concurrent.Character != 0 {
		return Result{}, fmt.Errorf("stuntgres: restore cannot execute concurrent index phases")
	}
	scope.Owner.Mu.Lock()
	explicit := scope.Owner.InBlock()
	scope.Owner.Mu.Unlock()
	var nameScratch [8]ids.NameKey
	for {
		var reservedNames []ids.NameKey
		if s.Name.Text == "" && s.Concurrent.Character == 0 {
			e.Mu.Lock()
			namespace := scope.LockedRef
			namespace.Address = scope.LockedDescriptor.(*catalog.Relation).Namespace()
			e.Mu.Unlock()
			var err error
			reservedNames, err = e.Locks.AppendRelationNameClaims(nameScratch[:0], scope.LockTicket, namespace)
			if err != nil {
				return Result{}, err
			}
		}
		e.Mu.Lock()
		fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
		if err := scope.RefreshRelationGrantsLocked(e); err != nil {
			return fail(err)
		}
		auth, err := e.StatementAuthorizationLocked(scope.Op, s)
		if err != nil {
			return fail(err)
		}
		if err = scope.CaptureIdentityLocked(auth); err != nil {
			return fail(err)
		}
		parent, ok := auth.Catalog.Relation(scope.LockedRef.Address)
		if !ok {
			return fail(ids.ErrCatalogReplaced)
		}
		base := auth.Catalog
		stage := e.BeginCatalogStage(base)
		stage.AuthorizeLocked(auth, scope.Op.Session)
		stage.Indexes().Output = scope.Output
		failStage := func(err error) (Result, error) {
			e.Mu.Unlock()
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		h := TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}
		created, err := index.PrepareCreate(h, auth, parent, s, scope.Input, reservedNames)
		if err != nil {
			return failStage(err)
		}
		key, ns, name := created.Key, created.Namespace, created.Key.Name
		if created.Skip {
			e.Mu.Unlock()
			e.DiscardCatalogStage(stage)
			scope.Op.ExistingRelationNotice(name)
			scope.Completed = true
			return Result{}, nil
		}
		d := created.Relation
		if s.Concurrent.Character != 0 {
			if err := e.VisibilityWriteLocked(scope.TxRef); err != nil {
				return failStage(err)
			}
		}
		err = index.StageCreate(h, parent, d, s.Only)
		e.Mu.Unlock()
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, index.DefaultClassDiagnostic(h, auth, err)
		}
		prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
		if err != nil {
			e.DiscardCatalogStage(stage)
			return Result{}, err
		}
		_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
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
			if contextErr := scope.Op.Context().Err(); contextErr != nil {
				return Result{}, contextErr
			}
			if s.Name.Text == "" {
				continue
			}
			if s.IfNotExists {
				e.Mu.Lock()
				currentAuth, authErr := e.StatementAuthorizationLocked(scope.Op, s)
				current := e.PrivateCatalogLocked(scope.Owner.Catalog)
				_, exists := current.LookupName(key)
				same := current.Epoch() == base.Epoch() && current.Get(parent.Address()) != nil
				if authErr == nil && same {
					authErr = currentAuth.Check(access.Requirement{Object: scope.LockedRef, OwnerOnly: true})
				}
				if authErr == nil && same {
					authErr = currentAuth.CheckCreate(ns)
				}
				e.Mu.Unlock()
				if authErr != nil {
					return Result{}, authErr
				}
				if same && exists {
					scope.Op.ExistingRelationNotice(name)
					scope.Completed = true
					return Result{}, nil
				}
			}
			return Result{}, SQLError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
		if err == nil && s.Concurrent.Character != 0 {
			e.Mu.Lock()
			e.Visibility.ResetPhaseLocked(scope.TxRef)
			e.Mu.Unlock()
			return executeConcurrentIndex(e, scope, s, d)
		}
		if err == nil {
			scope.Completed = true
		}
		return Result{}, err
	}
}
