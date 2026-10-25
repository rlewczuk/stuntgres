package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) collationCreationNamespaceLocked(auth *AuthorizationView, stage *CatalogStage, name parser.ObjectName) (ids.ObjectRef, error) {
	var changes session.Journal
	ref, err := e.creationNamespaceStagedLocked(*auth, name, false, &changes, true)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	for _, d := range changes.Descriptors {
		if err := stage.Create(d); err != nil {
			return ids.ObjectRef{}, err
		}
	}
	if len(changes.Descriptors) != 0 {
		auth.Catalog = e.StageRead(stage)
		auth.Recapture(auth.Catalog)
		if ns, ok := auth.Catalog.Namespace(ref.Address); ok && !ns.TemporaryToast && auth.Session != nil && ns.Temporary == auth.Session.SessionToken {
			auth.StagedTemporary = ns.Address()
		}
	}
	ns := auth.Catalog.MustNamespace(ref.Address)
	if ns.Temporary != (ids.SessionToken{}) && !access.OwnTemporaryNamespace(temporaryNamespace(*auth), ns) {
		actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
		if err != nil {
			return ids.ObjectRef{}, err
		}
		if !actor.Superuser {
			return ids.ObjectRef{}, SQLError("42501", "permission denied for schema "+ns.Name())
		}
	}
	return ref, nil
}

// Called by the runner, not a resource callback, after source transaction drain.
// A failed candidate leaves the namespace and every durable dependency intact.
// Engine.mu also owns the ending session's sequence memories and TEMP pointer:
// command drain does not exclude concurrent catalog replacement of live peers.
func (e *Engine) endTemporaryCollations(source *session.Session) (err error) {
	if session.PrivateSessionOf(source.SessionToken) != nil {
		return nil
	}
	var maintenance *session.Session
	for {
		e.Mu.Lock()
		base := e.Catalog.Current()
		var namespaces []*catalog.Namespace
		for _, d := range base.Descriptors() {
			if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary == source.SessionToken {
				namespaces = append(namespaces, ns)
			}
		}
		if len(namespaces) == 0 {
			PayloadOf(source).Sequences.ForgetLocked()
			source.TemporaryNamespace = ids.ObjectAddress{}
			e.Mu.Unlock()
			return nil
		}
		owned := make(map[ids.ObjectAddress]bool, len(namespaces))
		for _, ns := range namespaces {
			owned[ns.Address()] = true
		}
		children, captureErr := catalog.NamespaceChildren(base, owned)
		e.Mu.Unlock()
		if captureErr != nil {
			return captureErr
		}
		if maintenance == nil {
			maintenance, err = e.startSession(context.Background(), session.SessionStart{Authenticated: e.RoleStore.Domain.Principal(10)}, true)
			if err != nil {
				return err
			}
			defer func() {
				maintenance.RequestEnd(session.CleanClose)
				err = errors.Join(err, maintenance.WaitEnd(context.Background()))
			}()
		}
		stale, attemptErr := e.removeTemporaryNamespaceCandidate(maintenance, base, namespaces, children, source.SessionToken)
		if stale {
			continue
		}
		if attemptErr != nil {
			return attemptErr
		}
		e.Mu.Lock()
		PayloadOf(source).Sequences.ForgetLocked()
		source.TemporaryNamespace = ids.ObjectAddress{}
		e.Mu.Unlock()
		return nil
	}
}

func (e *Engine) removeTemporaryNamespaceCandidate(session *session.Session, base catalog.Read, namespaces []*catalog.Namespace, children []ids.ObjectAddress, token ids.SessionToken) (stale bool, err error) {
	var stage *CatalogStage
	// Registered first, so classification runs after Finish, stage discard and
	// Operation.Done have fully unwound the failed attempt.
	defer func() {
		if err != nil && stage != nil && stage.Lifecycle != catalogPublished &&
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
	if err = scope.Gate(&parser.DropCollation{Mode: parser.DropCascade}); err != nil {
		return false, err
	}
	stage = e.BeginCatalogStage(base)
	stage.terminalCleanup = true
	defer e.DiscardCatalogStage(stage)
	for _, ns := range namespaces {
		if ns.Temporary != token {
			return false, fmt.Errorf("stuntgres: foreign temporary namespace cleanup")
		}
		stage.DomainBases = append(stage.DomainBases, ns)
		for key := range base.State().DefaultACLs() {
			if key.Schema == ns.Address() {
				stage.SetDefaultACL(key, nil)
			}
		}
	}
	// Exclude owned targets before projecting their surviving dependents: a TEMP
	// table being removed must not also receive a replacement row shape.
	e.Mu.Lock()
	for _, a := range children {
		if err = stage.RemoveObjectLocked(e.Catalog.Domain.Ref(base.Epoch(), a), false); err != nil {
			break
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return false, err
	}
	if err = scope.StageExpressionDependents(stage, children, parser.DropCascade, nil); err != nil {
		return false, err
	}
	if err = scope.StageOwnedSequences(stage, children, parser.DropCascade, nil); err != nil {
		return false, err
	}
	var relations []ids.ObjectAddress
	for a := range stage.Removals() {
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
		if err = scope.AcquireSequence(e.Catalog.Domain.Ref(base.Epoch(), a), lock.AccessExclusive, true); err != nil {
			return false, err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return false, err
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.Completed = true
	}
	return false, err
}
