package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"stuntgres/internal/parser"
)

func (e *Engine) collationCreationNamespaceLocked(auth *AuthorizationView, stage *CatalogStage, name parser.ObjectName) (ObjectRef, error) {
	var changes catalogChanges
	ref, err := e.creationNamespaceStagedLocked(*auth, name, false, &changes, true)
	if err != nil {
		return ObjectRef{}, err
	}
	for _, d := range changes.descriptors {
		if err := stage.Create(d); err != nil {
			return ObjectRef{}, err
		}
	}
	if len(changes.descriptors) != 0 {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
	}
	ns := auth.catalog.state.descriptors[ref.Address].(*namespaceDescriptor)
	if ns.temporary != (SessionToken{}) && (auth.session == nil || ns.temporary != auth.session.token) {
		actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
		if err != nil {
			return ObjectRef{}, err
		}
		if !actor.Superuser {
			return ObjectRef{}, sqlError("42501", "permission denied for schema "+ns.name)
		}
	}
	return ref, nil
}

// Called by the runner, not a resource callback, after source transaction drain.
// A failed candidate leaves the namespace and every durable dependency intact.
func (e *Engine) endTemporaryCollations(source *Session) (err error) {
	if source.token.private != nil {
		return nil
	}
	var maintenance *Session
	for {
		e.mu.Lock()
		base := CatalogRead{e, e.state}
		var namespaces []*namespaceDescriptor
		for _, d := range base.state.descriptors {
			if ns, ok := d.(*namespaceDescriptor); ok && ns.temporary == source.token {
				namespaces = append(namespaces, ns)
			}
		}
		if len(namespaces) == 0 {
			e.mu.Unlock()
			source.sequences = nil
			source.lastSequence = ObjectAddress{}
			source.temporaryNamespace = ObjectAddress{}
			return nil
		}
		owned := make(map[ObjectAddress]bool, len(namespaces))
		for _, ns := range namespaces {
			owned[ns.address] = true
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
		source.sequences = nil
		source.lastSequence = ObjectAddress{}
		source.temporaryNamespace = ObjectAddress{}
		return nil
	}
}

func (e *Engine) removeTemporaryNamespaceCandidate(session *Session, base CatalogRead, namespaces []*namespaceDescriptor, children []ObjectAddress, token SessionToken) (stale bool, err error) {
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
		if ns.temporary != token {
			return false, fmt.Errorf("stuntgres: foreign temporary namespace cleanup")
		}
		stage.domainBases = append(stage.domainBases, ns)
	}
	// Exclude owned targets before projecting their surviving dependents: a TEMP
	// table being removed must not also receive a replacement row shape.
	e.mu.Lock()
	for _, a := range children {
		if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: a}, false); err != nil {
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
	var relations []ObjectAddress
	for a := range stage.removals {
		if a.ClassID == RelationClassID {
			relations = append(relations, a)
		}
	}
	for _, a := range children {
		if a.ClassID == RelationClassID {
			relations = append(relations, a)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].ObjectID < relations[j].ObjectID })
	for i, a := range relations {
		if i != 0 && a == relations[i-1] {
			continue
		}
		if err = scope.acquireSequence(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: a}, accessExclusive, true); err != nil {
			return false, err
		}
	}
	e.mu.Lock()
	for _, ns := range namespaces {
		if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: base.Epoch(), Address: ns.address}, true); err != nil {
			break
		}
	}
	e.mu.Unlock()
	if err != nil {
		return false, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return false, err
	}
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	return false, err
}
