package engine

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
)

// engineDomain is the domain of the engine that built auth, or nil for a view
// the engine did not build.
func engineDomain(auth AuthorizationView) *ids.Domain {
	if auth.inputs == nil {
		return nil
	}
	return auth.inputs.domain
}

func temporaryNamespace(auth AuthorizationView) ids.ObjectAddress {
	if auth.session == nil || auth.Catalog.State() == nil || auth.session.SessionToken == (ids.SessionToken{}) || auth.session.SessionToken.Domain() != engineDomain(auth) || session.PrivateSessionOf(auth.session.SessionToken) != nil {
		return ids.ObjectAddress{}
	}
	table := func(a ids.ObjectAddress) bool {
		ns, ok := auth.Catalog.Namespace(a)
		return ok && !ns.TemporaryToast && ns.Temporary == auth.session.SessionToken
	}
	if table(auth.stagedTemporary) {
		return auth.stagedTemporary
	}
	if table(auth.session.TemporaryNamespace) {
		return auth.session.TemporaryNamespace
	}
	if owner := auth.session.Transactions; owner != nil && owner.Catalog != nil {
		for a, d := range auth.Catalog.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == auth.session.SessionToken {
				return a
			}
		}
	}
	return ids.ObjectAddress{}
}

func ownTemporaryNamespace(auth AuthorizationView, ns *catalog.Namespace) bool {
	return ns != nil && !ns.TemporaryToast && ns.Temporary != (ids.SessionToken{}) && temporaryNamespace(auth) == ns.Address()
}
func (e *Engine) relationCreationNamespaceLocked(auth AuthorizationView, name parser.ObjectName, persistence string, c *session.Journal) (ids.ObjectRef, error) {
	return e.creationNamespaceStagedLocked(auth, name, persistence == "temporary", c, false)
}

func (e *Engine) creationNamespaceStagedLocked(auth AuthorizationView, name parser.ObjectName, temp bool, c *session.Journal, system bool) (ids.ObjectRef, error) {
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		return e.creationNamespaceWithSystem(auth, name, system)
	}
	temp = temp || len(name.Parts) == 2
	if !temp {
		var err error
		_, temp, err = explicitPath(auth)
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
func (e *Engine) temporaryNamespaceStagedLocked(auth AuthorizationView, c *session.Journal) (ids.ObjectRef, error) {
	if auth.session == nil || session.PrivateSessionOf(auth.session.SessionToken) != nil {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: private maintenance cannot create a temporary namespace")
	}
	auth.session.Transactions.AccessedTemporary = true
	if a := temporaryNamespace(auth); a != (ids.ObjectAddress{}) {
		return e.catalog.Domain.Ref(e.catalog.state.Epoch(), a), nil
	}
	rights, err := access.DatabaseRights(context.Background(), auth.Catalog, auth.Roles, auth.State.CurrentUser)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	if rights&authorization.TemporaryPrivilege == 0 {
		db := auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
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
	owner := e.roles.domain.Principal(10)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: fmt.Sprintf("pg_temp_%d", auth.session.SessionToken.ID()), ObjectRevision: rev, Owner: owner, Temporary: auth.session.SessionToken}
	toast := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: toastOID}, ObjectName: fmt.Sprintf("pg_toast_temp_%d", auth.session.SessionToken.ID()), ObjectRevision: toastRevision, Owner: owner, Temporary: auth.session.SessionToken, TemporaryToast: true}
	if c.Originals == nil {
		c.Originals = make(map[ids.ObjectAddress]catalog.Descriptor)
		c.Descriptors = make(map[ids.ObjectAddress]catalog.Descriptor)
		c.Names = make(map[ids.NameKey]ids.ObjectAddress)
	}
	e.catalogChangeLocked(c, ns.Address(), ns)
	e.catalogChangeLocked(c, toast.Address(), toast)
	return e.catalog.Domain.Ref(e.catalog.state.Epoch(), ns.Address()), nil
}

// Creation permission precedes persistence adjustment, including for SET SCHEMA.
// Callers supply the coherent catalog containing any privately staged namespace.
func adjustRelationCreationPersistence(auth AuthorizationView, ref ids.ObjectRef, requested string) (string, error) {
	ns := auth.Catalog.MustNamespace(ref.Address)
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
func (e *Engine) endTemporarySession(s *session.Session) error {
	if session.PrivateSessionOf(s.SessionToken) != nil {
		return nil
	}
	for {
		e.mu.Lock()
		base := e.catalog.read(e.catalog.state)
		for _, d := range base.Descriptors() {
			if _, ok := d.(*catalog.Collation); !ok {
				continue
			}
			if ns, ok := base.Namespace(d.Namespace()); ok && ns.Temporary == s.SessionToken {
				e.mu.Unlock()
				return nil
			}
		}
		var namespaces []ids.ObjectAddress
		owned := make(map[ids.ObjectAddress]bool)
		for a, d := range base.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == s.SessionToken {
				namespaces = append(namespaces, a)
				owned[a] = true
			}
		}
		if len(namespaces) == 0 {
			payloadOf(s).sequences.ForgetLocked()
			s.TemporaryNamespace = ids.ObjectAddress{}
			e.mu.Unlock()
			return nil
		}
		targets, err := catalog.NamespaceChildren(base, owned)
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
		store := e.storage.Relations[address]
		if store != shape.original || store == nil || store.Generation != shape.generation || store.Ends != shape.observedEnds || !storage.SameVersionRoot(store.Versions, shape.root) {
			return true
		}
	}
	for address, candidate := range stage.indexesOrZero().Candidates {
		if index, ok := stage.Get(address).(*catalog.Relation); ok && index.SQLIndex != nil && !candidate.Matches(e.storage.Relations[index.SQLIndex.Parent]) {
			return true
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.storage.IndexRoots[address] != root {
				return true
			}
		}
	}
	return !e.storageReadsValidLocked(stage)
}

// The whole-root fence includes replacement predecessors and newly introduced
// dependents. Row-only commits need not change that root: copy their current
// storage/generator/index maps at this final publication, not at preparation.
func (e *Engine) installTemporarySessionStageLocked(s *session.Session, stage *CatalogStage, namespaces []ids.ObjectAddress, indexes *temporarySessionIndexes) (bool, error) {
	if e.catalog.state != stage.Base.State() {
		return false, nil
	}
	for _, address := range namespaces {
		namespace, ok := e.catalog.state.Namespace(address)
		if !ok || namespace.Temporary != s.SessionToken {
			return false, nil
		}
	}
	for address, shape := range stage.rowShapes {
		store := e.storage.Relations[address]
		if store != shape.original || store.Generation != shape.generation || store.Ends != shape.observedEnds || !storage.SameVersionRoot(store.Versions, shape.root) {
			return false, nil
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.storage.IndexRoots[address] != root {
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
	sequences := make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.storage.Sequences))
	for a, g := range e.storage.Sequences {
		sequences[a] = g
	}
	relations := make(map[ids.ObjectAddress]*storage.Relation, len(e.storage.Relations))
	for a, store := range e.storage.Relations {
		relations[a] = store
	}
	roots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.storage.IndexRoots))
	for a, root := range e.storage.IndexRoots {
		roots[a] = root
	}
	for address, shape := range stage.rowShapes {
		relations[address] = shape.replacement
	}
	if indexes != nil {
		for address, root := range indexes.roots {
			roots[address] = root
		}
	}
	for a := range stage.Removals() {
		delete(sequences, a)
		delete(relations, a)
		delete(roots, a)
	}
	catalog.PruneNamespaceDefaults(stage.Base.State(), next, namespaces)
	nextRevision := e.catalog.nextRevision
	for _, descriptor := range stage.Descriptors() {
		nextRevision, _ = catalog.AdvanceRevisionFrontier(nextRevision, descriptor)
	}
	e.catalog.state = next
	e.storage.ReplaceSequencesLocked(sequences)
	e.storage.ReplaceRelationsLocked(relations)
	e.storage.ReplaceIndexRootsLocked(roots)
	stage.lifecycle = catalogPublished
	e.catalog.nextRevision = nextRevision
	payloadOf(s).sequences.ForgetLocked()
	s.TemporaryNamespace = ids.ObjectAddress{}
	return true, nil
}

type temporarySessionIndexes struct {
	previous map[ids.ObjectAddress]*storage.IndexRoot
	roots    map[ids.ObjectAddress]*storage.IndexRoot
}

// Capture affected roots under the mutex; construction remains outside the
// terminal install. Unrelated root changes are not fenced or overwritten.
func (e *Engine) prepareTemporarySessionIndexes(stage *CatalogStage) (*temporarySessionIndexes, error) {
	e.mu.Lock()
	if e.catalog.state != stage.Base.State() {
		e.mu.Unlock()
		return nil, catalog.Conflict{}
	}
	result := &temporarySessionIndexes{previous: make(map[ids.ObjectAddress]*storage.IndexRoot)}
	for address := range stage.Removals() {
		index, ok := stage.Base.Relation(address)
		if ok && index.SQLIndex != nil {
			result.previous[address] = e.storage.IndexRoots[address]
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
		result.previous[address] = e.storage.IndexRoots[address]
	}
	e.mu.Unlock()
	changes := session.NewJournal()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.catalog.read(next)}}
	for address, shape := range stage.rowShapes {
		parent, ok := next.Relation(address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if _, _, err := transportIndexLayouts(&evaluation, stage, changes, parent, shape.original, shape.replacement, nil, nil); err != nil {
			return nil, err
		}
	}
	result.roots = make(map[ids.ObjectAddress]*storage.IndexRoot, len(result.previous))
	for address := range result.previous {
		if _, removed := stage.Removal(address); !removed {
			idx, ok := next.Relation(address)
			if !ok || idx.SQLIndex == nil {
				return nil, ids.ErrCatalogReplaced
			}
			shape, ok := stage.rowShapes[idx.SQLIndex.Parent]
			candidate, produced := index.JournalIndexesOrZero(changes).Candidates[address]
			if !ok || !produced || !candidate.MatchesIndex(idx, shape.replacement) {
				return nil, ids.ErrCatalogReplaced
			}
			result.roots[address] = candidate.Root
		}
	}
	return result, nil
}
