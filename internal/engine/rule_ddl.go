package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// executeRule alternates canonical locked definition phases with unlocked lock
// admission. The partial definition and lexical manifests are statement-local;
// neither held handles nor the Host cache authority or wait under Engine.mu.
func executeRule(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.StatementAuthorizationLocked(scope.Op, stmt)
	if err != nil {
		return fail(err)
	}
	host := ViewHost{tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op}}}
	definition, err := view.BeginRule(host, auth, stmt, scope.LockedRef)
	if err != nil {
		return fail(err)
	}
	// Fresh catalog/provider views must retain the epoch that authorized the
	// partial definition, including BeginRule's event ownership decision.
	authRevision, roleRevision := auth.Revision, auth.RoleRecord.Revision
	event := scope.MutationTarget
	if creation, ok := stmt.(*parser.CreateRule); ok {
		lexical, err := e.AdmissionNameReference(auth, creation.Table)
		if err != nil {
			return fail(err)
		}
		// The spelling observation is independent of the granted event identity.
		// Later name validation may reject a shadow, but must never retarget it.
		event.AdmittedTargetName, event.AdmittedTargetLexical = creation.Table, lexical
		scope.MutationTarget = event
	}
	targets := []core.MutationTarget{event}
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
		fresh, err := e.StatementAuthorizationLocked(scope.Op, stmt)
		if err != nil {
			return err
		}
		if fresh.Revision != authRevision || fresh.RoleRecord.Revision != roleRevision {
			return fmt.Errorf("stuntgres: authorization changed before publication")
		}
		auth = fresh
		if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
			return ids.ErrCatalogReplaced
		}
		currentAdmission := scope.QueryLockAdmission
		defer func() {
			scope.MutationTarget = event
			scope.QueryLockAdmission = currentAdmission
		}()
		for _, target := range targets {
			scope.MutationTarget = target
			if err := scope.ValidateAdmissionNamesLocked(e, auth); err != nil {
				return err
			}
		}
		for _, admission := range admissions {
			scope.QueryLockAdmission = admission
			if err := scope.ValidateAdmissionNamesLocked(e, auth); err != nil {
				return err
			}
		}
		for _, source := range consumed {
			current, err := core.CurrentQuerySourceLocked(e, auth.Catalog, scope.Owner.Catalog, source)
			if err != nil {
				return err
			}
			if core.PreparedDefinitionStamp(current, scope.Op.Session) != core.PreparedDefinitionStamp(source.Relation, scope.Op.Session) {
				return ids.ErrCatalogReplaced
			}
		}
		return nil
	}
	// Called locked, returns locked. Capture actual bound dependencies before
	// waiting so a later grant cannot silently retarget an already frozen part.
	holdBinding := func(b analyze.Binding) error {
		refs := view.DefinitionReferences(b, event.LockedRef, scope.QuerySources)
		for _, source := range b.QuerySources {
			present, held := false, source.Ref == event.LockedRef
			for _, admitted := range scope.QuerySources {
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
				scope.QuerySources = append(scope.QuerySources, source)
			}
			retain(source)
		}
		for _, ref := range refs {
			d, ok := auth.Catalog.Relation(ref.Address)
			if !ok {
				return ids.ErrCatalogReplaced
			}
			retain(exec.Leaf{Ref: ref, Relation: d, Storage: e.PrivateStorageLocked(scope.Owner.Catalog, ref.Address)})
		}
		e.Mu.Unlock()
		err := acquireBoundViewRelations(scope, refs)
		var providers []exec.Leaf
		if err == nil {
			providers, err = scope.AcquireViewSources(true)
		}
		e.Mu.Lock()
		for _, source := range providers {
			retain(source)
		}
		if err != nil {
			return err
		}
		return fence()
	}
	if creation, ok := stmt.(*parser.CreateRule); ok {
		condition, err := definition.BindCondition(host, auth, scope.Input)
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
			lexical, err := e.AdmissionNameReference(auth, source.Table.Name)
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
		retain(exec.Leaf{Ref: event.LockedRef, Relation: event.LockedDescriptor.(*catalog.Relation), Storage: e.PrivateStorageLocked(scope.Owner.Catalog, event.LockedRef.Address)})
		if err := holdBinding(condition); err != nil {
			return fail(err)
		}
		for ordinal, action := range creation.Actions {
			e.Mu.Unlock()
			err := parser.VisitMutationTargets(action, func(command parser.Statement) error {
				scope.MutationTarget = core.MutationTarget{}
				defer func() { scope.MutationTarget = event }()
				if err := scope.AcquireRelationTarget(command, stmt, true, false); err != nil {
					return err
				}
				targets = append(targets, scope.MutationTarget)
				e.Mu.Lock()
				scope.QuerySources = append(scope.QuerySources, exec.Leaf{Ref: scope.LockedRef, Relation: scope.LockedDescriptor.(*catalog.Relation), Only: table.SourceOnly(command), Storage: e.PrivateStorageLocked(scope.Owner.Catalog, scope.LockedRef.Address)})
				e.Mu.Unlock()
				return nil
			})
			if err == nil {
				err = scope.AcquireQueryRelations(action, stmt, true)
				admissions = append(admissions, scope.QueryLockAdmission)
			}
			var providers []exec.Leaf
			if err == nil {
				providers, err = scope.AcquireViewSources(true)
			}
			e.Mu.Lock()
			for _, source := range providers {
				retain(source)
			}
			if err != nil {
				return fail(err)
			}
			if err := fence(); err != nil {
				return fail(err)
			}
			binding, err := definition.BindAction(host, auth, ordinal, scope.Input)
			if err != nil {
				return fail(err)
			}
			// Preserve every grant-time provider consumed by this action,
			// including typed targets and nested modifiers absent from leaves.
			for _, source := range scope.QuerySources {
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
	return publishRelationChangeLocked(e, scope, auth, change)
}

// publishRelationChangeLocked finishes a planned RULE, POLICY or TRIGGER change.
// Engine.mu is held on entry and released on every return. A skip ends in its
// fenced NOTICE; otherwise the change is staged with its query sources and,
// after the unlock, replaced, validated and published against the epoch of
// the relation reference.
func publishRelationChangeLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, change view.RelationChange) (core.Result, error) {
	if change.Skip != "" {
		return finishPolicyNoticeLocked(e, scope, change.Skip)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.TriggerReplacement = change.Replacement
	stage.QuerySources = append(stage.QuerySources, change.Sources...)
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	err := stage.Replace(change.Next)
	if err == nil {
		var prepared *core.PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.Op.Context(), stage)
		if err == nil {
			_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: change.Ref.Epoch})
		}
	}
	if err != nil {
		return core.Result{}, err
	}
	scope.Completed = true
	return core.Result{}, nil
}
