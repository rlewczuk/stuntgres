package core

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
	return auth.inputs.Domain
}

func temporaryNamespace(auth AuthorizationView) ids.ObjectAddress {
	if auth.Session == nil || auth.Catalog.State() == nil || auth.Session.SessionToken == (ids.SessionToken{}) || auth.Session.SessionToken.Domain() != engineDomain(auth) || session.PrivateSessionOf(auth.Session.SessionToken) != nil {
		return ids.ObjectAddress{}
	}
	table := func(a ids.ObjectAddress) bool {
		ns, ok := auth.Catalog.Namespace(a)
		return ok && !ns.TemporaryToast && ns.Temporary == auth.Session.SessionToken
	}
	if table(auth.StagedTemporary) {
		return auth.StagedTemporary
	}
	if table(auth.Session.TemporaryNamespace) {
		return auth.Session.TemporaryNamespace
	}
	if owner := auth.Session.Transactions; owner != nil && owner.Catalog != nil {
		for a, d := range auth.Catalog.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == auth.Session.SessionToken {
				return a
			}
		}
	}
	return ids.ObjectAddress{}
}

func (e *Engine) RelationCreationNamespaceLocked(auth AuthorizationView, name parser.ObjectName, persistence string, c *session.Journal) (ids.ObjectRef, error) {
	return e.creationNamespaceStagedLocked(auth, name, persistence == "temporary", c, false)
}

func (e *Engine) creationNamespaceStagedLocked(auth AuthorizationView, name parser.ObjectName, temp bool, c *session.Journal, system bool) (ids.ObjectRef, error) {
	if len(name.Parts) == 2 && name.Parts[0].Text != "pg_temp" {
		return e.creationNamespaceWithSystem(auth, name, system)
	}
	temp = temp || len(name.Parts) == 2
	if !temp {
		var err error
		_, temp, err = ExplicitPath(auth)
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
	if auth.Session == nil || session.PrivateSessionOf(auth.Session.SessionToken) != nil {
		return ids.ObjectRef{}, fmt.Errorf("stuntgres: private maintenance cannot create a temporary namespace")
	}
	auth.Session.Transactions.AccessedTemporary = true
	if a := temporaryNamespace(auth); a != (ids.ObjectAddress{}) {
		return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), a), nil
	}
	rights, err := access.DatabaseRights(context.Background(), auth.Catalog, auth.Roles, auth.State.CurrentUser)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	if rights&authorization.TemporaryPrivilege == 0 {
		db := auth.Catalog.MustDatabase(auth.Owner.DatabaseAddress())
		return ids.ObjectRef{}, SQLError("42501", fmt.Sprintf(`permission denied to create temporary tables in database "%s"`, db.Name()))
	}
	oid, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	rev, err := e.Catalog.RevisionLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	toastOID, err := e.Catalog.AllocateOIDLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	toastRevision, err := e.Catalog.RevisionLocked()
	if err != nil {
		return ids.ObjectRef{}, err
	}
	owner := e.RoleStore.Domain.Principal(10)
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: fmt.Sprintf("pg_temp_%d", auth.Session.SessionToken.ID()), ObjectRevision: rev, Owner: owner, Temporary: auth.Session.SessionToken}
	toast := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: toastOID}, ObjectName: fmt.Sprintf("pg_toast_temp_%d", auth.Session.SessionToken.ID()), ObjectRevision: toastRevision, Owner: owner, Temporary: auth.Session.SessionToken, TemporaryToast: true}
	if c.Originals == nil {
		c.Originals = make(map[ids.ObjectAddress]catalog.Descriptor)
		c.Descriptors = make(map[ids.ObjectAddress]catalog.Descriptor)
		c.Names = make(map[ids.NameKey]ids.ObjectAddress)
	}
	e.CatalogChangeLocked(c, ns.Address(), ns)
	e.CatalogChangeLocked(c, toast.Address(), toast)
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ns.Address()), nil
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
		e.Mu.Lock()
		base := e.Catalog.Current()
		var namespaces []ids.ObjectAddress
		owned := make(map[ids.ObjectAddress]bool)
		for a, ns := range base.TemporaryNamespaces() {
			if ns.Temporary == s.SessionToken {
				namespaces = append(namespaces, a)
				owned[a] = true
			}
		}
		// Temporary collations belong to endTemporaryCollations. Only a session
		// with a temporary namespace can own one.
		if len(namespaces) != 0 {
			for _, d := range base.Descriptors() {
				if _, ok := d.(*catalog.Collation); ok && owned[d.Namespace()] {
					e.Mu.Unlock()
					return nil
				}
			}
		}
		if len(namespaces) == 0 {
			PayloadOf(s).Sequences.ForgetLocked()
			s.TemporaryNamespace = ids.ObjectAddress{}
			e.Mu.Unlock()
			return nil
		}
		targets, err := catalog.NamespaceChildren(base, owned)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		stage := e.BeginCatalogStage(base)
		stage.terminalCleanup = true
		for _, a := range targets {
			if err = stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), a)); err != nil {
				break
			}
		}
		if err == nil {
			err = StageExpressionDependencies(context.Background(), stage, targets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{})
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
		e.Mu.Lock()
		installed := false
		if err == nil {
			installed, err = e.installTemporarySessionStageLocked(s, stage, namespaces, indexes)
		}
		e.Mu.Unlock()
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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if e.Catalog.State != stage.Base.State() {
		return true
	}
	for address, shape := range stage.RowShapes {
		store := e.Storage.Relations[address]
		if store != shape.Original || store == nil || store.Generation != shape.Generation || store.Ends != shape.ObservedEnds || !storage.SameVersionRoot(store.Versions, shape.Root) {
			return true
		}
	}
	for address, candidate := range stage.IndexesOrZero().Candidates {
		if index, ok := stage.Get(address).(*catalog.Relation); ok && index.SQLIndex != nil && !candidate.Matches(e.Storage.Relations[index.SQLIndex.Parent]) {
			return true
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.Storage.IndexRoots[address] != root {
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
	if e.Catalog.State != stage.Base.State() {
		return false, nil
	}
	for _, address := range namespaces {
		namespace, ok := e.Catalog.State.Namespace(address)
		if !ok || namespace.Temporary != s.SessionToken {
			return false, nil
		}
	}
	for address, shape := range stage.RowShapes {
		store := e.Storage.Relations[address]
		if store != shape.Original || store.Generation != shape.Generation || store.Ends != shape.ObservedEnds || !storage.SameVersionRoot(store.Versions, shape.Root) {
			return false, nil
		}
	}
	if indexes != nil {
		for address, root := range indexes.previous {
			if e.Storage.IndexRoots[address] != root {
				return false, nil
			}
		}
	}
	if len(stage.RowShapes) == 0 && stage.Lifecycle == CatalogOpen {
		if err := e.EvolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil); err != nil {
			return false, err
		}
	}
	next := e.StageRead(stage).State()
	sequences := make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.Storage.Sequences))
	for a, g := range e.Storage.Sequences {
		sequences[a] = g
	}
	relations := make(map[ids.ObjectAddress]*storage.Relation, len(e.Storage.Relations))
	for a, store := range e.Storage.Relations {
		relations[a] = store
	}
	roots := make(map[ids.ObjectAddress]*storage.IndexRoot, len(e.Storage.IndexRoots))
	for a, root := range e.Storage.IndexRoots {
		roots[a] = root
	}
	for address, shape := range stage.RowShapes {
		relations[address] = shape.Replacement
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
	nextRevision := catalog.RevisionFrontier(e.Catalog.NextRevision(), stage.Descriptors())
	e.Catalog.PublishLocked(next)
	e.Storage.ReplaceSequencesLocked(sequences)
	e.Storage.ReplaceRelationsLocked(relations)
	e.Storage.ReplaceIndexRootsLocked(roots)
	stage.Lifecycle = catalogPublished
	e.Catalog.InstallRevisionFrontierLocked(nextRevision)
	PayloadOf(s).Sequences.ForgetLocked()
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
	e.Mu.Lock()
	if e.Catalog.State != stage.Base.State() {
		e.Mu.Unlock()
		return nil, catalog.Conflict{}
	}
	result := &temporarySessionIndexes{previous: make(map[ids.ObjectAddress]*storage.IndexRoot)}
	for address := range stage.Removals() {
		index, ok := stage.Base.Relation(address)
		if ok && index.SQLIndex != nil {
			result.previous[address] = e.Storage.IndexRoots[address]
		}
	}
	if len(stage.RowShapes) == 0 {
		e.Mu.Unlock()
		return result, nil
	}
	if stage.Lifecycle == CatalogOpen {
		if err := e.EvolveStageIndexesLocked(stage, ids.ObjectAddress{}, nil); err != nil {
			e.Mu.Unlock()
			return nil, err
		}
	}
	next := e.StageRead(stage).State()
	parents := make(map[ids.ObjectAddress]bool, len(stage.RowShapes))
	for address, shape := range stage.RowShapes {
		if shape.Original == nil || shape.Replacement == nil {
			e.Mu.Unlock()
			return nil, ids.ErrCatalogReplaced
		}
		parents[address] = true
	}
	for address, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !parents[index.SQLIndex.Parent] || !index.SQLIndex.Ready {
			continue
		}
		result.previous[address] = e.Storage.IndexRoots[address]
	}
	e.Mu.Unlock()
	changes := session.NewJournal()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.Catalog.Read(next)}}
	for address, shape := range stage.RowShapes {
		parent, ok := next.Relation(address)
		if !ok {
			return nil, ids.ErrCatalogReplaced
		}
		if _, _, err := TransportIndexLayouts(&evaluation, stage, changes, parent, shape.Original, shape.Replacement, nil, nil); err != nil {
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
			shape, ok := stage.RowShapes[idx.SQLIndex.Parent]
			candidate, produced := index.JournalIndexesOrZero(changes).Candidates[address]
			if !ok || !produced || !candidate.MatchesIndex(idx, shape.Replacement) {
				return nil, ids.ErrCatalogReplaced
			}
			result.roots[address] = candidate.Root
		}
	}
	return result, nil
}
