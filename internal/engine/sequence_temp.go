package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func temporaryNamespace(auth AuthorizationView) ids.ObjectAddress {
	if auth.session == nil || auth.catalog.State() == nil || auth.session.token == (ids.SessionToken{}) || auth.session.token.Domain() != domainOf(auth.engine) || privateSessionOf(auth.session.token) != nil {
		return ids.ObjectAddress{}
	}
	table := func(a ids.ObjectAddress) bool {
		ns, ok := auth.catalog.Namespace(a)
		return ok && !ns.TemporaryToast && ns.Temporary == auth.session.token
	}
	if table(auth.stagedTemporary) {
		return auth.stagedTemporary
	}
	if table(auth.session.temporaryNamespace) {
		return auth.session.temporaryNamespace
	}
	if owner := auth.session.transactions; owner != nil && owner.catalog != nil {
		for a, d := range auth.catalog.Descriptors() {
			if ns, ok := d.(*namespaceDescriptor); ok && !ns.TemporaryToast && ns.Temporary == auth.session.token {
				return a
			}
		}
	}
	return ids.ObjectAddress{}
}

func ownTemporaryNamespace(auth AuthorizationView, ns *namespaceDescriptor) bool {
	return ns != nil && !ns.TemporaryToast && ns.Temporary != (ids.SessionToken{}) && temporaryNamespace(auth) == ns.Address()
}
func (e *Engine) relationCreationNamespaceLocked(auth AuthorizationView, name parser.ObjectName, persistence string, c *catalogChanges) (ids.ObjectRef, error) {
	return e.creationNamespaceStagedLocked(auth, name, persistence == "temporary", c, false)
}

func (e *Engine) creationNamespaceStagedLocked(auth AuthorizationView, name parser.ObjectName, temp bool, c *catalogChanges, system bool) (ids.ObjectRef, error) {
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		return e.creationNamespaceWithSystem(auth, name, system)
	}
	temp = temp || len(name.Parts) == 2
	if !temp {
		var err error
		_, temp, err = e.explicitPath(auth)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		if !temp {
			return e.creationNamespaceWithSystem(auth, name, system)
		}
	}
	return e.temporaryNamespaceStagedLocked(auth, c)
}

// Engine.mu is held. The caller owns the candidate and its publication.
func (e *Engine) temporaryNamespaceStagedLocked(auth AuthorizationView, c *catalogChanges) (ids.ObjectRef, error) {
	if auth.session == nil || privateSessionOf(auth.session.token) != nil {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: private maintenance cannot create a temporary namespace")
	}
	auth.session.transactions.accessedTemporary = true
	if a := temporaryNamespace(auth); a != (ids.ObjectAddress{}) {
		return e.catalog.Domain.Ref(e.catalog.state.Epoch(), a), nil
	}
	rights, err := databaseRights(context.Background(), auth.catalog, auth.roles, auth.state.CurrentUser)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	if rights&authorization.TemporaryPrivilege == 0 {
		db := auth.catalog.MustDatabase(catalog.CurrentDatabaseAddress)
		return ids.ObjectRef{}, sqlError("42501", fmt.Sprintf(`permission denied to create temporary tables in database "%s"`, db.Name()))
	}
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	rev, err := e.catalog.revisionLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	toastOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	toastRevision, err := e.catalog.revisionLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	owner := e.authorizationDomain.Principal(10)
	ns := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: fmt.Sprintf("pg_temp_%d", auth.session.token.ID()), ObjectRevision: rev, Owner: owner, Temporary: auth.session.token}
	toast := &namespaceDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: toastOID}, ObjectName: fmt.Sprintf("pg_toast_temp_%d", auth.session.token.ID()), ObjectRevision: toastRevision, Owner: owner, Temporary: auth.session.token, TemporaryToast: true}
	if c.originals == nil {
		c.originals = make(map[ids.ObjectAddress]Descriptor)
		c.descriptors = make(map[ids.ObjectAddress]Descriptor)
		c.names = make(map[ids.NameKey]ids.ObjectAddress)
	}
	e.catalogChangeLocked(c, ns.Address(), ns)
	e.catalogChangeLocked(c, toast.Address(), toast)
	return e.catalog.Domain.Ref(e.catalog.state.Epoch(), ns.Address()), nil
}

// Creation permission precedes persistence adjustment, including for SET SCHEMA.
// Callers supply the coherent catalog containing any privately staged namespace.
func adjustRelationCreationPersistence(auth AuthorizationView, ref ids.ObjectRef, requested string) (string, error) {
	ns := auth.catalog.MustNamespace(ref.Address)
	temporary := ns.Temporary != (ids.SessionToken{})
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
		base := e.catalog.read(e.catalog.state)
		for _, d := range base.Descriptors() {
			if _, ok := d.(*collationDescriptor); !ok {
				continue
			}
			if ns, ok := base.Namespace(d.Namespace()); ok && ns.Temporary == s.token {
				e.mu.Unlock()
				return nil
			}
		}
		var namespaces []ids.ObjectAddress
		owned := make(map[ids.ObjectAddress]bool)
		for a, d := range base.Descriptors() {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.Temporary == s.token {
				namespaces = append(namespaces, a)
				owned[a] = true
			}
		}
		if len(namespaces) == 0 {
			s.sequences = nil
			s.lastSequence = ids.ObjectAddress{}
			s.temporaryNamespace = ids.ObjectAddress{}
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
			if err = stage.Remove(e.catalog.Domain.Ref(base.Epoch(), a)); err != nil {
				break
			}
		}
		if err == nil {
			err = stageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{})
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
	if e.catalog.state != stage.Base.State() {
		return true
	}
	for address, shape := range stage.rowShapes {
		store := e.storage[address]
		if store != shape.original || store == nil || store.generation != shape.generation || store.ends != shape.observedEnds || !sameVersionRoot(store.versions, shape.root) {
			return true
		}
	}
	for address, candidate := range stage.indexCandidates {
		if index, ok := stage.Descriptors[address].(*catalog.Relation); ok && index.SQLIndex != nil && !candidate.matches(e.storage[index.SQLIndex.Parent]) {
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
func (e *Engine) installTemporarySessionStageLocked(s *Session, stage *CatalogStage, namespaces []ids.ObjectAddress, indexes *temporarySessionIndexes) (bool, error) {
	if e.catalog.state != stage.Base.State() {
		return false, nil
	}
	for _, address := range namespaces {
		namespace, ok := e.catalog.state.Namespace(address)
		if !ok || namespace.Temporary != s.token {
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
		if err := e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil); err != nil {
			return false, err
		}
	}
	next := e.stageRead(stage).State()
	sequences := make(map[ids.ObjectAddress]*sequenceGenerator, len(e.sequences))
	for a, g := range e.sequences {
		sequences[a] = g
	}
	storage := make(map[ids.ObjectAddress]*relationStorage, len(e.storage))
	for a, store := range e.storage {
		storage[a] = store
	}
	roots := make(map[ids.ObjectAddress]*logicalIndexRoot, len(e.indexRoots))
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
	for a := range stage.Removals {
		delete(sequences, a)
		delete(storage, a)
		delete(roots, a)
	}
	catalog.PruneNamespaceDefaults(stage.Base.State(), next, namespaces)
	nextRevision := e.catalog.nextRevision
	for _, descriptor := range stage.Descriptors {
		if revision := descriptorRevisionFrontier(descriptor); revision >= nextRevision {
			nextRevision = revision
			if nextRevision != ids.DefinitionRevision(^uint64(0)) {
				nextRevision++
			}
		}
	}
	e.catalog.state, e.sequences, e.storage, e.indexRoots = next, sequences, storage, roots
	stage.lifecycle = catalogPublished
	e.catalog.nextRevision = nextRevision
	s.sequences = nil
	s.lastSequence = ids.ObjectAddress{}
	s.temporaryNamespace = ids.ObjectAddress{}
	return true, nil
}

type temporarySessionIndexes struct {
	previous map[ids.ObjectAddress]*logicalIndexRoot
	roots    map[ids.ObjectAddress]*logicalIndexRoot
}

// Capture affected roots under the mutex; construction remains outside the
// terminal install. Unrelated root changes are not fenced or overwritten.
func (e *Engine) prepareTemporarySessionIndexes(stage *CatalogStage) (*temporarySessionIndexes, error) {
	e.mu.Lock()
	if e.catalog.state != stage.Base.State() {
		e.mu.Unlock()
		return nil, CatalogConflict{}
	}
	result := &temporarySessionIndexes{previous: make(map[ids.ObjectAddress]*logicalIndexRoot)}
	for address := range stage.Removals {
		index, ok := stage.Base.Relation(address)
		if ok && index.SQLIndex != nil {
			result.previous[address] = e.indexRoots[address]
		}
	}
	if len(stage.rowShapes) == 0 {
		e.mu.Unlock()
		return result, nil
	}
	if stage.lifecycle == catalogOpen {
		if err := e.evolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil); err != nil {
			e.mu.Unlock()
			return nil, err
		}
	}
	next := e.stageRead(stage).State()
	parents := make(map[ids.ObjectAddress]bool, len(stage.rowShapes))
	for address, shape := range stage.rowShapes {
		if shape.original == nil || shape.replacement == nil {
			e.mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		parents[address] = true
	}
	for address, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !parents[index.SQLIndex.Parent] || !index.SQLIndex.Ready {
			continue
		}
		result.previous[address] = e.indexRoots[address]
	}
	e.mu.Unlock()
	changes := newCatalogChanges()
	evaluation := EvalContext{Context: context.Background(), catalog: e.catalog.read(next)}
	for address, shape := range stage.rowShapes {
		parent, ok := next.Relation(address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if _, _, err := transportIndexLayouts(&evaluation, stage, changes, parent, shape.original, shape.replacement, nil, nil); err != nil {
			return nil, err
		}
	}
	result.roots = make(map[ids.ObjectAddress]*logicalIndexRoot, len(result.previous))
	for address := range result.previous {
		if _, removed := stage.Removals[address]; !removed {
			index, ok := next.Relation(address)
			if !ok || index.SQLIndex == nil {
				return nil, ids.ErrCatalogReplaced
			}
			shape, ok := stage.rowShapes[index.SQLIndex.Parent]
			candidate, produced := changes.indexCandidates[address]
			if !ok || !produced || !candidate.matchesIndex(index, shape.replacement) {
				return nil, ids.ErrCatalogReplaced
			}
			result.roots[address] = candidate.root
		}
	}
	return result, nil
}
