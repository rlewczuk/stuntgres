package engine

import (
	"fmt"
	"stuntgres/internal/parser"
)

// A supplied phase root is a detached candidate fenced against its exact source.
// Catalog publication alone owns installation, revision and root-map authority.
type indexRootCandidate struct {
	root       *logicalIndexRoot
	index      *relationDescriptor
	storage    *relationStorage
	generation uint64
	ends       *deletionRoot
	length     int
}

func (c indexRootCandidate) matches(store *relationStorage) bool {
	return store == c.storage && store != nil && store.generation == c.generation && store.ends == c.ends && len(store.versions) == c.length
}

func (c indexRootCandidate) matchesIndex(index *relationDescriptor, store *relationStorage) bool {
	return store != nil && c.index != nil && sameIndexKeySemantics(c.index, index) && c.matches(indexStorageIdentity(store))
}

func simpleConcurrentIndex(parent *relationDescriptor, definition *indexDefinition) (bool, error) {
	for _, key := range definition.keys {
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

func (e *Engine) executeConcurrentIndex(scope *StatementScope, syntax *parser.CreateIndex, initial *relationDescriptor) (Result, error) {
	if err := scope.waitIndexWriters(initial.address); err != nil {
		return Result{}, err
	}
	if _, err := e.publishConcurrentIndexRoot(scope, syntax, initial.address, true); err != nil {
		return Result{}, err
	}
	if err := scope.waitIndexWriters(initial.address); err != nil {
		return Result{}, err
	}
	xmin, err := e.publishConcurrentIndexRoot(scope, syntax, initial.address, false)
	if err != nil {
		return Result{}, err
	}
	if err := scope.waitOlderIndexSnapshots(xmin, initial.address); err != nil {
		return Result{}, err
	}
	if err := e.publishConcurrentIndexValid(scope, syntax, initial.address); err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}

func (e *Engine) concurrentIndexAuthorityLocked(scope *StatementScope, syntax *parser.CreateIndex, address ObjectAddress) (*relationDescriptor, *relationDescriptor, AuthorizationView, error) {
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
	index, ok := e.state.descriptors[address].(*relationDescriptor)
	if !ok || index.sqlIndex == nil || index.sqlIndex.parent != scope.lockedRef.Address {
		return nil, nil, auth, ErrCatalogReplaced
	}
	parent, ok := e.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	if !ok {
		return nil, nil, auth, ErrCatalogReplaced
	}
	if err = auth.CheckCreate(ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: parent.namespace}); err != nil {
		return nil, nil, auth, err
	}
	return index, parent, auth, nil
}

func (e *Engine) publishConcurrentIndexRoot(scope *StatementScope, syntax *parser.CreateIndex, address ObjectAddress, first bool) (uint64, error) {
	for {
		e.mu.Lock()
		index, parent, auth, err := e.concurrentIndexAuthorityLocked(scope, syntax, address)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		if index.sqlIndex.valid || !index.sqlIndex.live || index.sqlIndex.ready == first {
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
		safe, err := simpleConcurrentIndex(parent, index.sqlIndex)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		xmin, err := e.visibilitySnapshotLocked(scope.txRef, safe)
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		store := e.storage[parent.address]
		if store == nil {
			e.mu.Unlock()
			return 0, ErrCatalogReplaced
		}
		snapshot := *store
		next, definition := *index, *index.sqlIndex
		definition.ready = true
		next.sqlIndex = &definition
		next.revision, err = e.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return 0, err
		}
		stage := e.BeginCatalogStage(auth.catalog)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexParents = map[ObjectAddress]*relationDescriptor{parent.address: parent}
		namespace := auth.catalog.state.descriptors[parent.namespace].Name()
		e.mu.Unlock()
		evaluation := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, Input: scope.input, Output: scope.output, catalog: auth.catalog}
		root, err := buildIndexRoot(&evaluation, parent, &definition, &snapshot, nil)
		if err == nil {
			err = validateUniqueRoot(scope.op.Context(), parent, &next, root, namespace, scope.output, &auth, first)
		}
		if err != nil {
			e.DiscardCatalogStage(stage)
			return 0, err
		}
		stage.indexCandidates = map[ObjectAddress]indexRootCandidate{address: {root: root, index: &next, storage: store, generation: snapshot.generation, ends: snapshot.ends, length: len(snapshot.versions)}}
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

func (e *Engine) publishConcurrentIndexValid(scope *StatementScope, syntax *parser.CreateIndex, address ObjectAddress) error {
	for {
		e.mu.Lock()
		index, parent, auth, err := e.concurrentIndexAuthorityLocked(scope, syntax, address)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		if !index.sqlIndex.ready || !index.sqlIndex.live || index.sqlIndex.valid || e.indexRoots[address] == nil {
			e.mu.Unlock()
			return fmt.Errorf("stuntgres: invalid concurrent index validity phase")
		}
		if err = e.visibilityWriteLocked(scope.txRef); err != nil {
			e.mu.Unlock()
			return err
		}
		next, definition := *index, *index.sqlIndex
		definition.valid = true
		next.sqlIndex = &definition
		next.revision, err = e.revisionLocked()
		if err != nil {
			e.mu.Unlock()
			return err
		}
		stage := e.BeginCatalogStage(auth.catalog)
		stage.authorizeLocked(auth, scope.op.session)
		stage.indexParents = map[ObjectAddress]*relationDescriptor{parent.address: parent}
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
