package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
)

// PrepareStoredQueryLocked consumes Engine.mu on every return. Standalone view
// and refresh callers use the same complete preparation/materialization split.
func (e *Engine) PrepareStoredQueryLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (plan.Node, *exec.Source, error) {
	scope.rlsAdmissionDepth++
	prepared, err := e.prepareStoredSourceLocked(scope, ref, definition, invoker, mark, outputUses)
	err = e.endRowSecurityAdmissionLocked(scope, err)
	if err == nil {
		err = e.materializePolicySourcesLocked(scope)
	}
	if err != nil {
		e.Mu.Unlock()
		return nil, nil, err
	}
	if locks == nil {
		locks = scope.QueryLockContext
	}
	source, err := e.captureStoredSourceLocked(scope, prepared, locks)
	if err == nil {
		err = e.fenceRowSecurityProgramsLocked(scope)
	}
	if err != nil && source != nil {
		e.closeQuerySourceSetLocked(scope.Op.Context(), source, nil)
		source = nil
	}
	e.Mu.Unlock()
	return prepared.root, source, err
}

// Metadata only: provider preflight and the complete selected source closure
// finish before any tuple prefix is pinned. Returns with Engine.mu held.
func (e *Engine) prepareStoredSourceLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, outputUses []int) (*storedSourcePreparation, error) {
	ctx := scope.Op.Context()
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: HostOf(scope)}
	}
	auth, err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark)
	if err != nil {
		return nil, err
	}
	catalog := e.PrivateCatalogLocked(scope.PendingCatalog())
	consumer, ok := catalog.Relation(ref.Address)
	if !ok || stored.ViewOf(consumer) != definition {
		return nil, ids.ErrCatalogReplaced
	}
	e.Mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(scope), Catalog: catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.QuoteCaptures = scope.quoteCaptures
	evaluation.QuoteOrigin = exec.QuoteOrigin{Owner: ref, Definition: definition.RuleOID(), Revision: definition.QuoteRevision()}
	evaluation.RoutineRecipe, evaluation.RoutineConsumer = true, consumer
	evaluation.RoutineChecks = definition.Dependencies()
	evaluation.RoutineAuthority = &auth
	root, reads, err := view.PrepareStoredExecutionRoot(&evaluation, definition, outputUses)
	if err == nil {
		err = view.ValidateMarkedQueryJoin(&evaluation, root, catalog, nil, plan.RowMark{}, false, nil, reads)
	}
	e.Mu.Lock()
	if err != nil {
		return nil, err
	}
	if auth, err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		return nil, err
	}
	catalog = e.PrivateCatalogLocked(scope.PendingCatalog())
	consumer = catalog.MustRelation(ref.Address)
	relationPrincipal := consumer.Owner
	if definition.Invoker() {
		relationPrincipal = invoker
	}
	leaves := make([]exec.Leaf, len(definition.Sources()))
	for i, leaf := range definition.Sources() {
		relation := catalog.MustRelation(leaf.Ref.Address)
		use, err := stored.ViewUseReference(catalog, consumer, leaf.Ref)
		if err != nil {
			return nil, err
		}
		leaves[i] = exec.Leaf{Ref: use, Relation: relation, Only: leaf.Only, Principal: relationPrincipal, Storage: e.PrivateStorageLocked(scope.PendingCatalog(), leaf.Ref.Address), Mark: stored.ViewLeafRowMark(definition, i, mark), AnalysisOnly: leaf.AnalysisOnly}
		leaves[i].TableOIDRead = leaf.TableOIDRead
		leaves[i].Uses, err = analyze.QuerySourceReadUses(reads[i], relation.Attributes)
		if err != nil {
			return nil, err
		}
	}
	if scope.MaterializedOrZero().Refresh && ref == scope.LockedRef {
		if err := e.initializeQueryLocksLocked(scope, root, leaves); err != nil {
			return nil, err
		}
		scope.Inquiry.visibilityDemand = scope.Inquiry.visibilityDemand || definition.Query().VisibilitySnapshot
	}
	sources, err := e.prepareQuerySourcesLocked(scope, leaves, len(leaves) == 0, invoker, plan.RowMark{})
	if err != nil {
		return nil, err
	}
	return &storedSourcePreparation{root: root, definition: definition, consumer: consumer, sources: sources}, nil
}

func (e *Engine) captureStoredSourceLocked(scope *StatementScope, prepared *storedSourcePreparation, locks *exec.LockContext) (*exec.Source, error) {
	ctx := scope.Op.Context()
	definition, consumer, leaves := prepared.definition, prepared.consumer, prepared.sources.leaves
	source, err := e.capturePreparedQuerySourcesLocked(scope, prepared.sources, locks)
	e.Mu.Unlock()
	defer e.Mu.Lock()
	if err != nil {
		return nil, err
	}
	if err = exec.SelectQuerySources(ctx, source); err != nil {
		source.Close()
		return nil, err
	}
	source = view.FreezeStoredSources(source, definition, leaves, scope.authority, HostOf(scope))
	// Only this opaque stored tree borrows its defining recipe. User/grafted
	// replacements retain their existing per-node ancestry rules.
	source.RoutineRecipe = consumer
	if recipe := stored.ViewOf(consumer); recipe != nil {
		source.RoutineChecks = recipe.Dependencies()
	}
	return source, nil
}
