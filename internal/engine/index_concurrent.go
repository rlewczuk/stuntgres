package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/parser"
)

func executeConcurrentIndex(e *core.Engine, scope *core.StatementScope, syntax *parser.CreateIndex, initial *catalog.Relation) (core.Result, error) {
	if err := waitIndexWriters(scope, initial.Address()); err != nil {
		return core.Result{}, err
	}
	if _, err := publishConcurrentIndexRoot(e, scope, syntax, initial.Address(), true); err != nil {
		return core.Result{}, err
	}
	if err := waitIndexWriters(scope, initial.Address()); err != nil {
		return core.Result{}, err
	}
	xmin, err := publishConcurrentIndexRoot(e, scope, syntax, initial.Address(), false)
	if err != nil {
		return core.Result{}, err
	}
	if err := waitOlderIndexSnapshots(scope, xmin, initial.Address()); err != nil {
		return core.Result{}, err
	}
	if err := publishConcurrentIndexValid(e, scope, syntax, initial.Address()); err != nil {
		return core.Result{}, err
	}
	scope.Completed = true
	return core.Result{}, nil
}

func concurrentIndexAuthorityLocked(e *core.Engine, scope *core.StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress) (*catalog.Relation, *catalog.Relation, core.AuthorizationView, error) {
	if err := scope.RefreshRelationGrantsLocked(e); err != nil {
		return nil, nil, core.AuthorizationView{}, err
	}
	auth, err := e.StatementAuthorizationLocked(scope.Op, syntax)
	if err != nil {
		return nil, nil, auth, err
	}
	if err = scope.CaptureIdentityLocked(auth); err != nil {
		return nil, nil, auth, err
	}
	index, ok := e.Catalog.State.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != scope.LockedRef.Address {
		return nil, nil, auth, ids.ErrCatalogReplaced
	}
	parent, ok := e.Catalog.State.Relation(index.SQLIndex.Parent)
	if !ok {
		return nil, nil, auth, ids.ErrCatalogReplaced
	}
	if err = auth.CheckCreate(e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), parent.Namespace())); err != nil {
		return nil, nil, auth, err
	}
	return index, parent, auth, nil
}

func publishConcurrentIndexRoot(e *core.Engine, scope *core.StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress, first bool) (uint64, error) {
	for {
		e.Mu.Lock()
		idx, parent, auth, err := concurrentIndexAuthorityLocked(e, scope, syntax, address)
		if err != nil {
			e.Mu.Unlock()
			return 0, err
		}
		if idx.SQLIndex.Valid || !idx.SQLIndex.Live || idx.SQLIndex.Ready == first {
			e.Mu.Unlock()
			return 0, fmt.Errorf("stuntgres: invalid concurrent index phase")
		}
		if err = e.VisibilityWriteLocked(scope.TxRef); err != nil {
			e.Mu.Unlock()
			return 0, err
		}
		// Every retry captures snapshot ownership with the actual immutable row root.
		e.Visibility.WithdrawSnapshotLocked(scope.TxRef)
		safe, err := index.SimpleConcurrentIndex(parent, idx.SQLIndex)
		if err != nil {
			e.Mu.Unlock()
			return 0, err
		}
		xmin, err := e.VisibilitySnapshotLocked(scope.TxRef, safe)
		if err != nil {
			e.Mu.Unlock()
			return 0, err
		}
		store := e.Storage.Relations[parent.Address()]
		if store == nil {
			e.Mu.Unlock()
			return 0, ids.ErrCatalogReplaced
		}
		snapshot := *store
		next, definition := *idx, *idx.SQLIndex
		definition.Ready = true
		next.SQLIndex = &definition
		next.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err != nil {
			e.Mu.Unlock()
			return 0, err
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, scope.Op.Session)
		stage.Indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		namespace := auth.Catalog.Get(parent.Namespace()).Name()
		e.Mu.Unlock()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: core.HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.RoutineAuthority = &auth
		root, err := index.BuildIndexRoot(&evaluation, parent, &definition, &snapshot, nil)
		if err == nil {
			err = index.ValidateUniqueRoot(scope.Op.Context(), parent, &next, root, namespace, scope.Output, &auth.View, core.DisclosureEnv(&auth), first)
		}
		if err != nil {
			e.DiscardCatalogStage(stage)
			return 0, err
		}
		stage.Indexes().Candidates = map[ids.ObjectAddress]index.RootCandidate{address: {Root: root, Index: &next, Storage: store, Generation: snapshot.Generation, Ends: snapshot.Ends, Length: len(snapshot.Versions)}}
		err = stage.Replace(&next)
		if err == nil {
			var prepared *core.PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.Op.Context(), stage)
			if err == nil {
				_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(catalog.Conflict); conflict {
			if err := scope.Op.Context().Err(); err != nil {
				return 0, err
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		if first {
			e.Mu.Lock()
			e.Visibility.ResetPhaseLocked(scope.TxRef)
			e.Mu.Unlock()
		}
		return xmin, nil
	}
}

func publishConcurrentIndexValid(e *core.Engine, scope *core.StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress) error {
	for {
		e.Mu.Lock()
		index, parent, auth, err := concurrentIndexAuthorityLocked(e, scope, syntax, address)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		if !index.SQLIndex.Ready || !index.SQLIndex.Live || index.SQLIndex.Valid || e.Storage.IndexRoots[address] == nil {
			e.Mu.Unlock()
			return fmt.Errorf("stuntgres: invalid concurrent index validity phase")
		}
		if err = e.VisibilityWriteLocked(scope.TxRef); err != nil {
			e.Mu.Unlock()
			return err
		}
		next, definition := *index, *index.SQLIndex
		definition.Valid = true
		next.SQLIndex = &definition
		next.ObjectRevision, err = e.Catalog.RevisionLocked()
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.AuthorizeLocked(auth, scope.Op.Session)
		stage.Indexes().Parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		e.Mu.Unlock()
		err = stage.Replace(&next)
		if err == nil {
			var prepared *core.PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.Op.Context(), stage)
			if err == nil {
				_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(catalog.Conflict); conflict {
			if err := scope.Op.Context().Err(); err != nil {
				return err
			}
			continue
		}
		if err == nil {
			e.Mu.Lock()
			e.Visibility.ResetPhaseLocked(scope.TxRef)
			e.Mu.Unlock()
		}
		return err
	}
}
