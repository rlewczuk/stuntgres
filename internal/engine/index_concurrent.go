package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// A supplied phase root is a detached candidate fenced against its exact source.
// Catalog publication alone owns installation, revision and root-map authority.
type indexRootCandidate struct {
	root       *storage.IndexRoot
	index      *catalog.Relation
	storage    *storage.Relation
	generation uint64
	ends       *storage.DeletionRoot
	length     int
}

func (c indexRootCandidate) matches(store *storage.Relation) bool {
	return store == c.storage && store != nil && store.Generation == c.generation && store.Ends == c.ends && len(store.Versions) == c.length
}

func (c indexRootCandidate) matchesIndex(index *catalog.Relation, store *storage.Relation) bool {
	return store != nil && c.index != nil && sameIndexKeySemantics(c.index, index) && c.matches(store.IndexIdentity())
}

func simpleConcurrentIndex(parent *catalog.Relation, definition *catalog.IndexDefinition) (bool, error) {
	for _, key := range definition.Keys {
		_, node, err := indexCatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		if node != nil {
			return false, nil
		}
	}
	return true, nil
}

func (e *Engine) executeConcurrentIndex(scope *StatementScope, syntax *parser.CreateIndex, initial *catalog.Relation) (Result, error) {
	if err := scope.waitIndexWriters(initial.Address()); err != nil {
		return Result{}, err
	}
	if _, err := e.publishConcurrentIndexRoot(scope, syntax, initial.Address(), true); err != nil {
		return Result{}, err
	}
	if err := scope.waitIndexWriters(initial.Address()); err != nil {
		return Result{}, err
	}
	xmin, err := e.publishConcurrentIndexRoot(scope, syntax, initial.Address(), false)
	if err != nil {
		return Result{}, err
	}
	if err := scope.waitOlderIndexSnapshots(xmin, initial.Address()); err != nil {
		return Result{}, err
	}
	if err := e.publishConcurrentIndexValid(scope, syntax, initial.Address()); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}

func (e *Engine) concurrentIndexAuthorityLocked(scope *StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress) (*catalog.Relation, *catalog.Relation, AuthorizationView, error) {
	if err := scope.refreshRelationGrantsLocked(e); err != nil {
		return nil, nil, AuthorizationView{}, err
	}
	auth, err := e.statementAuthorizationLocked(scope.op, syntax)
	if err != nil {
		return nil, nil, auth, err
	}
	if err = scope.captureIdentityLocked(auth); err != nil {
		return nil, nil, auth, err
	}
	index, ok := e.catalog.state.Relation(address)
	if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != scope.lockedRef.Address {
		return nil, nil, auth, ids.ErrCatalogReplaced
	}
	parent, ok := e.catalog.state.Relation(index.SQLIndex.Parent)
	if !ok {
		return nil, nil, auth, ids.ErrCatalogReplaced
	}
	if err = auth.CheckCreate(e.catalog.Domain.Ref(e.catalog.state.Epoch(), parent.Namespace())); err != nil {
		return nil, nil, auth, err
	}
	return index, parent, auth, nil
}

func (e *Engine) publishConcurrentIndexRoot(scope *StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress, first bool) (uint64, error) {
	for {
		e.mu.Lock()
		index, parent, auth, err := e.concurrentIndexAuthorityLocked(scope, syntax, address)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		if index.SQLIndex.Valid || !index.SQLIndex.Live || index.SQLIndex.Ready == first {
			e.mu.Unlock()
			return 0, fmt.Errorf("stuntgres: invalid concurrent index phase")
		}
		if err = e.visibilityWriteLocked(scope.txRef); err != nil {
			e.mu.Unlock()
			return 0, err
		}
		// Every retry captures snapshot ownership with the actual immutable row root.
		e.visibility.WithdrawSnapshotLocked(scope.txRef)
		safe, err := simpleConcurrentIndex(parent, index.SQLIndex)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		xmin, err := e.visibilitySnapshotLocked(scope.txRef, safe)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		store := e.storage.Relations[parent.Address()]
		if store == nil {
			e.mu.Unlock()
			return 0, ids.ErrCatalogReplaced
		}
		snapshot := *store
		next, definition := *index, *index.SQLIndex
		definition.Ready = true
		next.SQLIndex = &definition
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.authorizeLocked(auth, scope.op.Session)
		stage.indexes().parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		namespace := auth.Catalog.Get(parent.Namespace()).Name()
		e.mu.Unlock()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Input: scope.input, Output: scope.output, Catalog: auth.Catalog}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		evaluation.RoutineAuthority = &auth
		root, err := buildIndexRoot(&evaluation, parent, &definition, &snapshot, nil)
		if err == nil {
			err = validateUniqueRoot(scope.op.Context(), parent, &next, root, namespace, scope.output, &auth, first)
		}
		if err != nil {
			e.DiscardCatalogStage(stage)
			return 0, err
		}
		stage.indexes().candidates = map[ids.ObjectAddress]indexRootCandidate{address: {root: root, index: &next, storage: store, generation: snapshot.Generation, ends: snapshot.Ends, length: len(snapshot.Versions)}}
		err = stage.Replace(&next)
		if err == nil {
			var prepared *PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(catalog.Conflict); conflict {
			if err := scope.op.Context().Err(); err != nil {
				return 0, err
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		if first {
			e.mu.Lock()
			e.visibility.ResetPhaseLocked(scope.txRef)
			e.mu.Unlock()
		}
		return xmin, nil
	}
}

func (e *Engine) publishConcurrentIndexValid(scope *StatementScope, syntax *parser.CreateIndex, address ids.ObjectAddress) error {
	for {
		e.mu.Lock()
		index, parent, auth, err := e.concurrentIndexAuthorityLocked(scope, syntax, address)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if !index.SQLIndex.Ready || !index.SQLIndex.Live || index.SQLIndex.Valid || e.storage.IndexRoots[address] == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: invalid concurrent index validity phase")
		}
		if err = e.visibilityWriteLocked(scope.txRef); err != nil {
			e.mu.Unlock()
			return err
		}
		next, definition := *index, *index.SQLIndex
		definition.Valid = true
		next.SQLIndex = &definition
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		stage := e.BeginCatalogStage(auth.Catalog)
		stage.authorizeLocked(auth, scope.op.Session)
		stage.indexes().parents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		e.mu.Unlock()
		err = stage.Replace(&next)
		if err == nil {
			var prepared *PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(catalog.Conflict); conflict {
			if err := scope.op.Context().Err(); err != nil {
				return err
			}
			continue
		}
		if err == nil {
			e.mu.Lock()
			e.visibility.ResetPhaseLocked(scope.txRef)
			e.mu.Unlock()
		}
		return err
	}
}
