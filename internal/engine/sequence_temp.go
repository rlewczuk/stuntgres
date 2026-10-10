package engine

import (
	"context"
	"fmt"
	"maps"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func temporaryNamespace(auth AuthorizationView) ObjectAddress {
	if auth.session == nil {
		return ObjectAddress{}
	}
	if auth.stagedTemporary != (ObjectAddress{}) {
		if ns, ok := auth.catalog.state.descriptors[auth.stagedTemporary].(*namespaceDescriptor); ok && ns.temporary == auth.session.token {
			return ns.address
		}
	}
	a := auth.session.temporaryNamespace
	if a != (ObjectAddress{}) {
		if d, ok := auth.catalog.state.descriptors[a].(*namespaceDescriptor); ok && d.temporary == auth.session.token {
			return a
		}
	}
	if owner := auth.session.transactions; owner != nil && owner.catalog != nil {
		for a, d := range owner.catalog.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == auth.session.token {
				return a
			}
		}
	}
	return ObjectAddress{}
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
	if auth.session == nil || auth.session.token.private != nil {
		return ObjectRef{}, fmt.Errorf("stuntgres: private maintenance cannot create a temporary namespace")
	}
	auth.session.transactions.accessedTemporary = true
	if a := temporaryNamespace(auth); a != (ObjectAddress{}) {
		return ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: a}, nil
	}
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	rev, err := e.revisionLocked()
	if err != nil {
		return ObjectRef{}, err
	}
	ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: oid}, name: fmt.Sprintf("pg_temp_%d", auth.session.token.id), revision: rev, owner: auth.state.CurrentUser, temporary: auth.session.token}
	if c.originals == nil {
		c.originals = make(map[ObjectAddress]Descriptor)
		c.descriptors = make(map[ObjectAddress]Descriptor)
		c.names = make(map[NameKey]ObjectAddress)
	}
	e.catalogChangeLocked(c, ns.address, ns)
	return ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: ns.address}, nil
}

// Creation permission precedes persistence adjustment, including for SET SCHEMA.
// Callers supply the coherent catalog containing any privately staged namespace.
func adjustRelationCreationPersistence(auth AuthorizationView, ref ObjectRef, requested string) (string, error) {
	ns := auth.catalog.state.descriptors[ref.Address].(*namespaceDescriptor)
	temporary := ns.temporary != (SessionToken{})
	own := temporary && auth.session != nil && ns.temporary == auth.session.token
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
func (e *Engine) endTemporarySession(s *Session) {
	if s.token.private != nil {
		return
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
				return
			}
		}
		var namespaces, targets []ObjectAddress
		for a, d := range base.state.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == s.token {
				namespaces = append(namespaces, a)
			}
		}
		if len(namespaces) == 0 {
			s.sequences = nil
			s.lastSequence = ObjectAddress{}
			s.temporaryNamespace = ObjectAddress{}
			e.mu.Unlock()
			return
		}
		for a, d := range base.state.descriptors {
			for _, namespace := range namespaces {
				if d.Namespace() == namespace {
					targets = append(targets, a)
					break
				}
			}
		}
		e.mu.Unlock()
		stage := e.BeginCatalogStage(base)
		stage.terminalCleanup = true
		var err error
		for _, a := range targets {
			if err = stage.Remove(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: a}); err != nil {
				break
			}
		}
		if err == nil {
			err = stageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil)
		}
		if err == nil {
			err = stageOwnedExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil)
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
		e.DiscardCatalogStage(stage)
		if installed {
			return
		}
	}
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
	if len(stage.rowShapes) == 0 {
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
	for _, a := range namespaces {
		delete(next.names, relationNameKey(next.descriptors[a]))
		delete(next.descriptors, a)
	}
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
	if err := e.evolveStageIndexesLocked(stage, ObjectAddress{}, nil); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	next := e.stageRead(stage).state
	stores := make(map[ObjectAddress]*relationStorage, len(stage.rowShapes))
	parents := make(map[ObjectAddress]bool, len(stage.rowShapes))
	for address, shape := range stage.rowShapes {
		stores[address] = shape.replacement
		parents[address] = true
	}
	var rebuild map[ObjectAddress]*relationDescriptor
	for address, descriptor := range next.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !parents[index.sqlIndex.parent] || !index.sqlIndex.ready {
			continue
		}
		result.previous[address] = e.indexRoots[address]
		// Explicit intent also rebuilds ready-invalid keys without changing
		// readiness/validity/live flags or attempting index recovery.
		if !index.sqlIndex.valid {
			if rebuild == nil {
				rebuild = make(map[ObjectAddress]*relationDescriptor)
			}
			rebuild[address] = index
		}
	}
	roots := maps.Clone(e.indexRoots)
	e.mu.Unlock()
	prepared, err := e.prepareIndexCatalog(context.Background(), stage.base.state, next, stores, roots, parents, rebuild, sqltype.OutputEnvironment{}, nil)
	if err != nil {
		return nil, err
	}
	result.roots = make(map[ObjectAddress]*logicalIndexRoot, len(result.previous))
	for address := range result.previous {
		if _, removed := stage.removals[address]; !removed {
			result.roots[address] = prepared[address]
		}
	}
	return result, nil
}
