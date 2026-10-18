package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
)

func (e *Engine) collationCreationNamespaceLocked(auth *AuthorizationView, stage *CatalogStage, name parser.ObjectName) (ids.ObjectRef, error) {
	var changes catalogChanges
	ref, err := e.creationNamespaceStagedLocked(*auth, name, false, &changes, true)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	for _, d := range changes.descriptors {
		if err := stage.Create(d); err != nil {
			return ids.ObjectRef{}, err
		}
	}
	if len(changes.descriptors) != 0 {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
		if ns, ok := auth.catalog.Namespace(ref.Address); ok && !ns.TemporaryToast && auth.session != nil && ns.Temporary == auth.session.token {
			auth.stagedTemporary = ns.Address()
		}
	}
	ns := auth.catalog.MustNamespace(ref.Address)
	if ns.Temporary != (ids.SessionToken{}) && !ownTemporaryNamespace(*auth, ns) {
		actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		if !actor.Superuser {
			return ids.ObjectRef{}, sqlError("42501", "permission denied for schema "+ns.Name())
		}
	}
	return ref, nil
}

// Called by the runner, not a resource callback, after source transaction drain.
// A failed candidate leaves the namespace and every durable dependency intact.
// Engine.mu also owns the ending session's sequence memories and TEMP pointer:
// command drain does not exclude concurrent catalog replacement of live peers.
func (e *Engine) endTemporaryCollations(source *Session) (err error) {
	if privateSessionOf(source.token) != nil {
		return nil
	}
	var maintenance *Session
	for {
		e.mu.Lock()
		base := e.catalog.read(e.catalog.state)
		var namespaces []*catalog.Namespace
		for _, d := range base.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == source.token {
				namespaces = append(namespaces, ns)
			}
		}
		if len(namespaces) == 0 {
			source.sequences = nil
			source.lastSequence = ids.ObjectAddress{}
			source.temporaryNamespace = ids.ObjectAddress{}
			e.mu.Unlock()
			return nil
		}
		owned := make(map[ids.ObjectAddress]bool, len(namespaces))
		for _, ns := range namespaces {
			owned[ns.Address()] = true
		}
		children, captureErr := namespaceChildren(base, owned)
		e.mu.Unlock()
		if captureErr != nil {
			return captureErr
		}
		if maintenance == nil {
			maintenance, err = e.startSession(context.Background(), SessionStart{Authenticated: e.authorizationDomain.Principal(10)}, true)
			if err != nil {
				return err
			}
			defer func() {
				maintenance.RequestEnd(CleanClose)
				err = errors.Join(err, maintenance.WaitEnd(context.Background()))
			}()
		}
		stale, attemptErr := e.removeTemporaryNamespaceCandidate(maintenance, base, namespaces, children, source.token)
		if stale {
			continue
		}
		if attemptErr != nil {
			return attemptErr
		}
		e.mu.Lock()
		source.sequences = nil
		source.lastSequence = ids.ObjectAddress{}
		source.temporaryNamespace = ids.ObjectAddress{}
		e.mu.Unlock()
		return nil
	}
}

func (e *Engine) removeTemporaryNamespaceCandidate(session *Session, base CatalogRead, namespaces []*catalog.Namespace, children []ids.ObjectAddress, token ids.SessionToken) (stale bool, err error) {
	var stage *CatalogStage
	// Registered first, so classification runs after Finish, stage discard and
	// Operation.Done have fully unwound the failed attempt.
	defer func() {
		if err != nil && stage != nil && stage.lifecycle != catalogPublished &&
			!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			stale = e.temporaryCleanupStale(stage, nil)
		}
	}()
	op, err := session.Acquire(context.Background())
	if err != nil {
		return false, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return false, err
	}
	defer func() { err = scope.Finish(err) }()
	if err = scope.gate(&parser.DropCollation{Mode: parser.DropCascade}); err != nil {
		return false, err
	}
	stage = e.BeginCatalogStage(base)
	stage.terminalCleanup = true
	defer e.DiscardCatalogStage(stage)
	for _, ns := range namespaces {
		if ns.Temporary != token {
			return false, fmt.Errorf("stuntgres: foreign temporary namespace cleanup")
		}
		stage.domainBases = append(stage.domainBases, ns)
		for key := range base.State().DefaultACLs() {
			if key.Schema == ns.Address() {
				if stage.DefaultACLs == nil {
					stage.DefaultACLs = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
				}
				stage.DefaultACLs[key] = nil
			}
		}
	}
	// Exclude owned targets before projecting their surviving dependents: a TEMP
	// table being removed must not also receive a replacement row shape.
	e.mu.Lock()
	for _, a := range children {
		if err = stage.removeObjectLocked(e.catalog.Domain.Ref(base.Epoch(), a), false); err != nil {
			break
		}
	}
	e.mu.Unlock()
	if err != nil {
		return false, err
	}
	if err = scope.stageExpressionDependents(stage, children, parser.DropCascade, nil); err != nil {
		return false, err
	}
	if err = scope.stageOwnedSequences(stage, children, parser.DropCascade, nil); err != nil {
		return false, err
	}
	var relations []ids.ObjectAddress
	for a := range stage.Removals {
		if a.ClassID == ids.RelationClassID {
			relations = append(relations, a)
		}
	}
	for _, a := range children {
		if a.ClassID == ids.RelationClassID {
			relations = append(relations, a)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].ObjectID < relations[j].ObjectID })
	for i, a := range relations {
		if i != 0 && a == relations[i-1] {
			continue
		}
		if err = scope.acquireSequence(e.catalog.Domain.Ref(base.Epoch(), a), lock.AccessExclusive, true); err != nil {
			return false, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return false, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	return false, err
}
