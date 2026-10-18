package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
)

func temporaryNamespace(auth AuthorizationView) ObjectAddress {
	if auth.session == nil || auth.catalog.state == nil || auth.session.token == (SessionToken{}) || auth.session.token.domain != domainOf(auth.engine) || privateSessionOf(auth.session.token) != nil {
		return ObjectAddress{}
	}
	table := func(a ObjectAddress) bool {
		ns, ok := auth.catalog.state.descriptors[a].(*namespaceDescriptor)
		return ok && !ns.temporaryToast && ns.temporary == auth.session.token
	}
	if table(auth.stagedTemporary) {
		return auth.stagedTemporary
	}
	if table(auth.session.temporaryNamespace) {
		return auth.session.temporaryNamespace
	}
	if owner := auth.session.transactions; owner != nil && owner.catalog != nil {
		for a, d := range auth.catalog.state.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && !ns.temporaryToast && ns.temporary == auth.session.token {
				return a
			}
		}
	}
	return ObjectAddress{}
}

func ownTemporaryNamespace(auth AuthorizationView, ns *namespaceDescriptor) bool {
	return ns != nil && !ns.temporaryToast && ns.temporary != (SessionToken{}) && temporaryNamespace(auth) == ns.address
}
func (e *Engine) relationCreationNamespaceLocked(auth AuthorizationView, name parser.ObjectName, persistence string, c *catalogChanges) (ObjectRef, error) {
	return e.creationNamespaceStagedLocked(auth, name, persistence == "temporary", c, false)
}

func (e *Engine) creationNamespaceStagedLocked(auth AuthorizationView, name parser.ObjectName, temp bool, c *catalogChanges, system bool) (ObjectRef, error) {
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		return e.creationNamespaceWithSystem(auth, name, system)
	}
	temp = temp || len(name.Parts) == 2
	if !temp {
		var err error
		_, temp, err = e.explicitPath(auth)
		if err != nil {
			return ObjectRef{}, err
		}
		if !temp {
			return e.creationNamespaceWithSystem(auth, name, system)
		}
	}
	return e.temporaryNamespaceStagedLocked(auth, c)
}

// Engine.mu is held. The caller owns the candidate and its publication.
func (e *Engine) temporaryNamespaceStagedLocked(auth AuthorizationView, c *catalogChanges) (ObjectRef, error) {
	if auth.session == nil || privateSessionOf(auth.session.token) != nil {
		return ObjectRef{}, fmt.Errorf("stuntgres: private maintenance cannot create a temporary namespace")
	}
	auth.session.transactions.accessedTemporary = true
	if a := temporaryNamespace(auth); a != (ObjectAddress{}) {
		return ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: a}, nil
	}
	rights, err := databaseRights(context.Background(), auth.catalog, auth.roles, auth.state.CurrentUser)
	if err != nil {
		return ObjectRef{}, err
	}
	if rights&authorization.TemporaryPrivilege == 0 {
		db := auth.catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
		return ObjectRef{}, sqlError("42501", fmt.Sprintf(`permission denied to create temporary tables in database "%s"`, db.name))
	}
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	rev, err := e.revisionLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	toastOID, err := e.allocateOIDLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	toastRevision, err := e.revisionLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	owner := e.authorizationDomain.Principal(10)
	ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: oid}, name: fmt.Sprintf("pg_temp_%d", auth.session.token.id), revision: rev, owner: owner, temporary: auth.session.token}
	toast := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: toastOID}, name: fmt.Sprintf("pg_toast_temp_%d", auth.session.token.id), revision: toastRevision, owner: owner, temporary: auth.session.token, temporaryToast: true}
	if c.originals == nil {
		c.originals = make(map[ObjectAddress]Descriptor)
		c.descriptors = make(map[ObjectAddress]Descriptor)
		c.names = make(map[NameKey]ObjectAddress)
	}
	e.catalogChangeLocked(c, ns.address, ns)
	e.catalogChangeLocked(c, toast.address, toast)
	return ObjectRef{owner: e.domain, Epoch: e.state.epoch, Address: ns.address}, nil
}

// Creation permission precedes persistence adjustment, including for SET SCHEMA.
// Callers supply the coherent catalog containing any privately staged namespace.
func adjustRelationCreationPersistence(auth AuthorizationView, ref ObjectRef, requested string) (string, error) {
	ns := auth.catalog.state.descriptors[ref.Address].(*namespaceDescriptor)
	temporary := ns.temporary != (SessionToken{})
	own := ownTemporaryNamespace(auth, ns)
	switch requested {
	case "temporary":
		if own {
			return requested, nil
		}
		if temporary {
			return "", sqlError("42P16", "cannot create relations in temporary schemas of other sessions")
		}
		return "", sqlError("42P16", "cannot create temporary relation in non-temporary schema")
	case "logged":
		if own {
			return "temporary", nil
		}
		if temporary {
			return "", sqlError("42P16", "cannot create relations in temporary schemas of other sessions")
		}
	default:
		if temporary {
			return "", sqlError("42P16", "only temporary relations may be created in temporary schemas")
		}
	}
	return requested, nil
}

// Physical-end cleanup occurs after command drain. Catalog edits remain coherent
// and remove only this session's temporary objects, never a peer's namespace.
// Logical namespace retirement is not a PostgreSQL namespace tuple deletion:
// physical backend exit preserves pg_namespace and cleans only temp relations.
// Keep peer analysis generation/epoch unchanged, including at scalar exhaustion.
func (e *Engine) endTemporarySession(s *Session) error {
	if privateSessionOf(s.token) != nil {
		return nil
	}
	for {
		e.mu.Lock()
		base := CatalogRead{e, e.state}
		for _, d := range base.state.descriptors {
			if _, ok := d.(*collationDescriptor); !ok {
				continue
			}
			if ns, ok := base.state.descriptors[d.Namespace()].(*namespaceDescriptor); ok && ns.temporary == s.token {
				e.mu.Unlock()
				return nil
			}
		}
		var namespaces []ObjectAddress
		owned := make(map[ObjectAddress]bool)
		for a, d := range base.state.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == s.token {
				namespaces = append(namespaces, a)
				owned[a] = true
			}
		}
		if len(namespaces) == 0 {
			s.sequences = nil
			s.lastSequence = ObjectAddress{}
			s.temporaryNamespace = ObjectAddress{}
			e.mu.Unlock()
			return nil
		}
		targets, err := namespaceChildren(base, owned)
		e.mu.Unlock()
		if err != nil {
			return err
		}
		stage := e.BeginCatalogStage(base)
		stage.terminalCleanup = true
		for _, a := range targets {
			if err = stage.Remove(ObjectRef{owner: e.domain, Epoch: base.Epoch(), Address: a}); err != nil {
				break
			}
		}
		if err == nil {
			err = stageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil, ObjectAddress{})
		}
		if err == nil {
			err = stageOwnedExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil)
		}
		if err == nil {
			_, err = e.ValidateCatalogStage(context.Background(), stage)
		}
		var indexes *temporarySessionIndexes
		if err == nil {
			indexes, err = e.prepareTemporarySessionIndexes(stage)
		}
		e.mu.Lock()
		installed := false
		if err == nil {
			installed, err = e.installTemporarySessionStageLocked(s, stage, namespaces, indexes)
		}
		e.mu.Unlock()
		stale := err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && e.temporaryCleanupStale(stage, indexes)
		e.DiscardCatalogStage(stage)
		if err != nil {
			if stale {
				continue
			}
			return err
		}
		if installed {
			return nil
		}
	}
}

// Retry only a candidate whose captured retirement authority has actually
// changed. A structural failure against the same roots must escape teardown.
func (e *Engine) temporaryCleanupStale(stage *CatalogStage, indexes *temporarySessionIndexes) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != stage.base.state {
		return true
	}
	for address, shape := range stage.rowShapes {
		store := e.storage[address]
		if store != shape.original || store == nil || store.generation != shape.generation || store.ends != shape.observedEnds || !sameVersionRoot(store.versions, shape.root) {
			return true
		}
	}
	for address, candidate := range stage.indexCandidates {
		if index, ok := stage.descriptors[address].(*relationDescriptor); ok && index.sqlIndex != nil && !candidate.matches(e.storage[index.sqlIndex.parent]) {
			return true
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.indexRoots[address] != root {
				return true
			}
		}
	}
	return !e.partitionReadsValidLocked(stage)
}

// The whole-root fence includes replacement predecessors and newly introduced
// dependents. Row-only commits need not change that root: copy their current
// storage/generator/index maps at this final publication, not at preparation.
func (e *Engine) installTemporarySessionStageLocked(s *Session, stage *CatalogStage, namespaces []ObjectAddress, indexes *temporarySessionIndexes) (bool, error) {
	if e.state != stage.base.state {
		return false, nil
	}
	for _, address := range namespaces {
		namespace, ok := e.state.descriptors[address].(*namespaceDescriptor)
		if !ok || namespace.temporary != s.token {
			return false, nil
		}
	}
	for address, shape := range stage.rowShapes {
		store := e.storage[address]
		if store != shape.original || store.generation != shape.generation || store.ends != shape.observedEnds || !sameVersionRoot(store.versions, shape.root) {
			return false, nil
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.indexRoots[address] != root {
				return false, nil
			}
		}
	}
	if len(stage.rowShapes) == 0 && stage.lifecycle == catalogOpen {
		if err := e.evolveStageIndexesLocked(stage, ObjectAddress{}, nil); err != nil {
			return false, err
		}
	}
	next := e.stageRead(stage).state
	sequences := make(map[ObjectAddress]*sequenceGenerator, len(e.sequences))
	for a, g := range e.sequences {
		sequences[a] = g
	}
	storage := make(map[ObjectAddress]*relationStorage, len(e.storage))
	for a, store := range e.storage {
		storage[a] = store
	}
	roots := make(map[ObjectAddress]*logicalIndexRoot, len(e.indexRoots))
	for a, root := range e.indexRoots {
		roots[a] = root
	}
	for address, shape := range stage.rowShapes {
		storage[address] = shape.replacement
	}
	if indexes != nil {
		for address, root := range indexes.roots {
			roots[address] = root
		}
	}
	for a := range stage.removals {
		delete(sequences, a)
		delete(storage, a)
		delete(roots, a)
	}
	pruneNamespaceDefaults(stage.base.state, next, namespaces)
	nextRevision := e.nextRevision
	for _, descriptor := range stage.descriptors {
		if revision := descriptorRevisionFrontier(descriptor); revision >= nextRevision {
			nextRevision = revision
			if nextRevision != DefinitionRevision(^uint64(0)) {
				nextRevision++
			}
		}
	}
	e.state, e.sequences, e.storage, e.indexRoots = next, sequences, storage, roots
	stage.lifecycle = catalogPublished
	e.nextRevision = nextRevision
	s.sequences = nil
	s.lastSequence = ObjectAddress{}
	s.temporaryNamespace = ObjectAddress{}
	return true, nil
}

type temporarySessionIndexes struct {
	previous map[ObjectAddress]*logicalIndexRoot
	roots    map[ObjectAddress]*logicalIndexRoot
}

// Capture affected roots under the mutex; construction remains outside the
// terminal install. Unrelated root changes are not fenced or overwritten.
func (e *Engine) prepareTemporarySessionIndexes(stage *CatalogStage) (*temporarySessionIndexes, error) {
	e.mu.Lock()
	if e.state != stage.base.state {
		e.mu.Unlock()
		return nil, CatalogConflict{}
	}
	result := &temporarySessionIndexes{previous: make(map[ObjectAddress]*logicalIndexRoot)}
	for address := range stage.removals {
		index, ok := stage.base.state.descriptors[address].(*relationDescriptor)
		if ok && index.sqlIndex != nil {
			result.previous[address] = e.indexRoots[address]
		}
	}
	if len(stage.rowShapes) == 0 {
		e.mu.Unlock()
		return result, nil
	}
	if stage.lifecycle == catalogOpen {
		if err := e.evolveStageIndexesLocked(stage, ObjectAddress{}, nil); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	next := e.stageRead(stage).state
	parents := make(map[ObjectAddress]bool, len(stage.rowShapes))
	for address, shape := range stage.rowShapes {
		if shape.original == nil || shape.replacement == nil {
			e.mu.Unlock()
			return nil, ErrCatalogReplaced
		}
		parents[address] = true
	}
	for address, descriptor := range next.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !parents[index.sqlIndex.parent] || !index.sqlIndex.ready {
			continue
		}
		result.previous[address] = e.indexRoots[address]
	}
	e.mu.Unlock()
	changes := newCatalogChanges()
	evaluation := EvalContext{Context: context.Background(), catalog: CatalogRead{owner: e, state: next}}
	for address, shape := range stage.rowShapes {
		parent, ok := next.descriptors[address].(*relationDescriptor)
		if !ok {
			return nil, ErrCatalogReplaced
		}
		if _, _, err := transportIndexLayouts(&evaluation, stage, changes, parent, shape.original, shape.replacement, nil, nil); err != nil {
			return nil, err
		}
	}
	result.roots = make(map[ObjectAddress]*logicalIndexRoot, len(result.previous))
	for address := range result.previous {
		if _, removed := stage.removals[address]; !removed {
			index, ok := next.descriptors[address].(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				return nil, ErrCatalogReplaced
			}
			shape, ok := stage.rowShapes[index.sqlIndex.parent]
			candidate, produced := changes.indexCandidates[address]
			if !ok || !produced || !candidate.matchesIndex(index, shape.replacement) {
				return nil, ErrCatalogReplaced
			}
			result.roots[address] = candidate.root
		}
	}
	return result, nil
}
