package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// executeRule alternates canonical locked definition phases with unlocked lock
// admission. The partial definition and lexical manifests are statement-local;
// neither held handles nor the Host cache authority or wait under Engine.mu.
func (e *Engine) executeRule(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	host := ddlHost{e: e, op: scope.op}
	definition, err := view.BeginRule(host, auth, stmt, scope.lockedRef)
	if err != nil {
		return fail(err)
	}
	// Fresh catalog/provider views must retain the epoch that authorized the
	// partial definition, including BeginRule's event ownership decision.
	authRevision, roleRevision := auth.Revision, auth.RoleRecord.Revision
	event := scope.mutationTarget
	if creation, ok := stmt.(*parser.CreateRule); ok {
		lexical, err := e.admissionNameReference(auth, creation.Table)
		if err != nil {
			return fail(err)
		}
		// The spelling observation is independent of the granted event identity.
		// Later name validation may reject a shadow, but must never retarget it.
		event.admittedTargetName, event.admittedTargetLexical = creation.Table, lexical
		scope.mutationTarget = event
	}
	targets := []mutationTarget{event}
	var admissions []analyze.LockAdmission
	var consumed []exec.Leaf
	retain := func(source exec.Leaf) {
		for _, prior := range consumed {
			if prior.Ref == source.Ref && prior.Relation == source.Relation && prior.Storage == source.Storage {
				return
			}
		}
		consumed = append(consumed, source)
	}
	// Every unlocked gap fences all previously consumed providers, not just
	// the last action. Restore individual manifests: query-local indices and
	// ordering cannot be merged into another action's admission.
	fence := func() error {
		fresh, err := e.statementAuthorizationLocked(scope.op, stmt)
		if err != nil {
			return err
		}
		if fresh.Revision != authRevision || fresh.RoleRecord.Revision != roleRevision {
			return fmt.Errorf("stuntgres: authorization changed before publication")
		}
		auth = fresh
		if scope.txRef.Epoch() != e.catalog.State.Epoch() {
			return ids.ErrCatalogReplaced
		}
		currentAdmission := scope.queryLockAdmission
		defer func() {
			scope.mutationTarget = event
			scope.queryLockAdmission = currentAdmission
		}()
		for _, target := range targets {
			scope.mutationTarget = target
			if err := scope.validateAdmissionNamesLocked(e, auth); err != nil {
				return err
			}
		}
		for _, admission := range admissions {
			scope.queryLockAdmission = admission
			if err := scope.validateAdmissionNamesLocked(e, auth); err != nil {
				return err
			}
		}
		for _, source := range consumed {
			current, err := currentQuerySourceLocked(e, auth.Catalog, scope.owner.Catalog, source)
			if err != nil {
				return err
			}
			if preparedDefinitionStamp(current, scope.op.Session) != preparedDefinitionStamp(source.Relation, scope.op.Session) {
				return ids.ErrCatalogReplaced
			}
		}
		return nil
	}
	// Called locked, returns locked. Capture actual bound dependencies before
	// waiting so a later grant cannot silently retarget an already frozen part.
	holdBinding := func(b analyze.Binding) error {
		refs := view.DefinitionReferences(b, event.lockedRef, scope.querySources)
		for _, source := range b.QuerySources {
			present, held := false, source.Ref == event.lockedRef
			for _, admitted := range scope.querySources {
				if admitted.Ref == source.Ref {
					held = true
					if admitted.Relation == source.Relation && admitted.Storage == source.Storage && admitted.Only == source.Only {
						present = true
					}
				}
			}
			for _, ref := range refs {
				if ref == source.Ref {
					held = true
					break
				}
			}
			if !held {
				refs = append(refs, source.Ref)
			}
			if !present {
				scope.querySources = append(scope.querySources, source)
			}
			retain(source)
		}
		for _, ref := range refs {
			d, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			retain(exec.Leaf{Ref: ref, Relation: d, Storage: e.privateStorageLocked(scope.owner.Catalog, ref.Address)})
		}
		e.mu.Unlock()
		err := scope.acquireBoundViewRelations(refs)
		var providers []exec.Leaf
		if err == nil {
			providers, err = scope.acquireViewSources(true)
		}
		e.mu.Lock()
		for _, source := range providers {
			retain(source)
		}
		if err != nil {
			return err
		}
		return fence()
	}
	if creation, ok := stmt.(*parser.CreateRule); ok {
		condition, err := definition.BindCondition(host, auth, scope.input)
		if err != nil {
			return fail(err)
		}
		// Qualification sources come from the actual canonical capture, not
		// a synthetic admission query. Retain lexical observations separately
		// from their bound provider identities, like ordinary query admission.
		var qualification analyze.LockAdmission
		for _, source := range condition.QuerySources {
			if source.Table == nil {
				continue
			}
			lexical, err := e.admissionNameReference(auth, source.Table.Name)
			if err != nil {
				return fail(err)
			}
			if qualification.TableQueries == nil {
				qualification.TableQueries = make(map[*parser.TableReference]analyze.PreparedSource)
			}
			qualification.TableQueries[source.Table] = analyze.PreparedSource{Query: source.Query, Ref: source.Ref, Lexical: lexical}
		}
		if len(qualification.TableQueries) != 0 {
			admissions = append(admissions, qualification)
		}
		retain(exec.Leaf{Ref: event.lockedRef, Relation: event.lockedDescriptor.(*catalog.Relation), Storage: e.privateStorageLocked(scope.owner.Catalog, event.lockedRef.Address)})
		if err := holdBinding(condition); err != nil {
			return fail(err)
		}
		for ordinal, action := range creation.Actions {
			e.mu.Unlock()
			err := parser.VisitMutationTargets(action, func(command parser.Statement) error {
				scope.mutationTarget = mutationTarget{}
				defer func() { scope.mutationTarget = event }()
				if err := scope.acquireRelationTarget(command, stmt, true, false); err != nil {
					return err
				}
				targets = append(targets, scope.mutationTarget)
				e.mu.Lock()
				scope.querySources = append(scope.querySources, exec.Leaf{Ref: scope.lockedRef, Relation: scope.lockedDescriptor.(*catalog.Relation), Only: table.SourceOnly(command), Storage: e.privateStorageLocked(scope.owner.Catalog, scope.lockedRef.Address)})
				e.mu.Unlock()
				return nil
			})
			if err == nil {
				err = scope.acquireQueryRelations(action, stmt, true)
				admissions = append(admissions, scope.queryLockAdmission)
			}
			var providers []exec.Leaf
			if err == nil {
				providers, err = scope.acquireViewSources(true)
			}
			e.mu.Lock()
			for _, source := range providers {
				retain(source)
			}
			if err != nil {
				return fail(err)
			}
			if err := fence(); err != nil {
				return fail(err)
			}
			binding, err := definition.BindAction(host, auth, ordinal, scope.input)
			if err != nil {
				return fail(err)
			}
			// Preserve every grant-time provider consumed by this action,
			// including typed targets and nested modifiers absent from leaves.
			for _, source := range scope.querySources {
				retain(source)
			}
			if err := holdBinding(binding); err != nil {
				return fail(err)
			}
		}
	}
	change, err := definition.Finish(host, auth)
	if err != nil {
		return fail(err)
	}
	for _, source := range consumed {
		present := false
		for _, descriptor := range change.Sources {
			if descriptor == source.Relation {
				present = true
				break
			}
		}
		if !present {
			change.Sources = append(change.Sources, source.Relation)
		}
	}
	return e.publishRelationChangeLocked(scope, auth, change)
}

// publishRelationChangeLocked finishes a planned RULE, POLICY or TRIGGER change.
// Engine.mu is held on entry and released on every return. A skip ends in its
// fenced NOTICE; otherwise the change is staged with its query sources and,
// after the unlock, replaced, validated and published against the epoch of
// the relation reference.
func (e *Engine) publishRelationChangeLocked(scope *StatementScope, auth AuthorizationView, change view.RelationChange) (Result, error) {
	if change.Skip != "" {
		return e.finishPolicyNoticeLocked(scope, change.Skip)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.triggerReplacement = change.Replacement
	stage.querySources = append(stage.querySources, change.Sources...)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	err := stage.Replace(change.Next)
	if err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: change.Ref.Epoch})
		}
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
