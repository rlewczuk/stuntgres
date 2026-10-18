package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// A supplied phase root is a detached candidate fenced against its exact source.
// Catalog publication alone owns installation, revision and root-map authority.
type indexRootCandidate struct {
	root       *logicalIndexRoot
	index      *catalog.Relation
	storage    *relationStorage
	generation uint64
	ends       *deletionRoot
	length     int
}

func (c indexRootCandidate) matches(store *relationStorage) bool {
	return store == c.storage && store != nil && store.generation == c.generation && store.ends == c.ends && len(store.versions) == c.length
}

func (c indexRootCandidate) matchesIndex(index *catalog.Relation, store *relationStorage) bool {
	return store != nil && c.index != nil && sameIndexKeySemantics(c.index, index) && c.matches(indexStorageIdentity(store))
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
		visibility := e.visibility[scope.txRef]
		visibility.snapshot = false
		e.visibility[scope.txRef] = visibility
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
		store := e.storage[parent.Address()]
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
		stage := e.BeginCatalogStage(auth.catalog)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexParents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		namespace := auth.catalog.Get(parent.Namespace()).Name()
		e.mu.Unlock()
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: auth.catalog}
		evaluation.routineAuthority = &auth
		root, err := buildIndexRoot(&evaluation, parent, &definition, &snapshot, nil)
		if err == nil {
			err = validateUniqueRoot(scope.op.Context(), parent, &next, root, namespace, scope.output, &auth, first)
		}
		if err != nil {
			e.DiscardCatalogStage(stage)
			return 0, err
		}
		stage.indexCandidates = map[ids.ObjectAddress]indexRootCandidate{address: {root: root, index: &next, storage: store, generation: snapshot.generation, ends: snapshot.ends, length: len(snapshot.versions)}}
		err = stage.Replace(&next)
		if err == nil {
			var prepared *PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(CatalogConflict); conflict {
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
			e.resetVisibilityPhaseLocked(scope.txRef)
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
		if !index.SQLIndex.Ready || !index.SQLIndex.Live || index.SQLIndex.Valid || e.indexRoots[address] == nil {
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
		stage := e.BeginCatalogStage(auth.catalog)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexParents = map[ids.ObjectAddress]*catalog.Relation{parent.Address(): parent}
		e.mu.Unlock()
		err = stage.Replace(&next)
		if err == nil {
			var prepared *PreparedCatalogPublication
			prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
			if err == nil {
				_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.catalog.Epoch()})
			}
		}
		e.DiscardCatalogStage(stage)
		if _, conflict := err.(CatalogConflict); conflict {
			if err := scope.op.Context().Err(); err != nil {
				return err
			}
			continue
		}
		if err == nil {
			e.mu.Lock()
			e.resetVisibilityPhaseLocked(scope.txRef)
			e.mu.Unlock()
		}
		return err
	}
}
