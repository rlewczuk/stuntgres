package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

// prepareStoredQueryLocked consumes Engine.mu on every return. Standalone view
// and refresh callers use the same complete preparation/materialization split.
func (e *Engine) prepareStoredQueryLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (plan.Node, *exec.Source, error) {
	scope.rlsAdmissionDepth++
	prepared, err := e.prepareStoredSourceLocked(scope, ref, definition, invoker, mark, outputUses)
	err = e.endRowSecurityAdmissionLocked(scope, err)
	if err == nil {
		err = e.materializePolicySourcesLocked(scope)
	}
	if err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	if locks == nil {
		locks = scope.queryLocks
	}
	source, err := e.captureStoredSourceLocked(scope, prepared, locks)
	if err == nil {
		err = e.fenceRowSecurityProgramsLocked(scope)
	}
	if err != nil && source != nil {
		e.closeQuerySourceSetLocked(scope.op.Context(), source, nil)
		source = nil
	}
	e.mu.Unlock()
	return prepared.root, source, err
}

// Metadata only: provider preflight and the complete selected source closure
// finish before any tuple prefix is pinned. Returns with Engine.mu held.
func (e *Engine) prepareStoredSourceLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, outputUses []int) (*storedSourcePreparation, error) {
	ctx := scope.op.Context()
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: hostOf(scope)}
	}
	auth, err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark)
	if err != nil {
		return nil, err
	}
	catalog := e.privateCatalogLocked(scope.owner.Catalog)
	consumer, ok := catalog.Relation(ref.Address)
	if !ok || stored.ViewOf(consumer) != definition {
		return nil, ids.ErrCatalogReplaced
	}
	e.mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Catalog: catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.QuoteCaptures = scope.quoteCaptures
	evaluation.QuoteOrigin = exec.QuoteOrigin{Owner: ref, Definition: definition.RuleOID(), Revision: definition.QuoteRevision()}
	evaluation.RoutineRecipe, evaluation.RoutineConsumer = true, consumer
	evaluation.RoutineChecks = definition.Dependencies()
	evaluation.RoutineAuthority = &auth
	root, reads, err := view.PrepareStoredExecutionRoot(&evaluation, definition, outputUses)
	if err == nil {
		err = view.ValidateMarkedQueryJoin(&evaluation, root, catalog, nil, plan.RowMark{}, false, nil, reads)
	}
	e.mu.Lock()
	if err != nil {
		return nil, err
	}
	if auth, err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		return nil, err
	}
	catalog = e.privateCatalogLocked(scope.owner.Catalog)
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
		leaves[i] = exec.Leaf{Ref: use, Relation: relation, Only: leaf.Only, Principal: relationPrincipal, Storage: e.privateStorageLocked(scope.owner.Catalog, leaf.Ref.Address), Mark: stored.ViewLeafRowMark(definition, i, mark), AnalysisOnly: leaf.AnalysisOnly}
		leaves[i].Uses, err = analyze.QuerySourceReadUses(reads[i], relation.Attributes)
		if err != nil {
			return nil, err
		}
	}
	if scope.materializedOrZero().Refresh && ref == scope.lockedRef {
		if err := e.initializeQueryLocksLocked(scope, root, leaves); err != nil {
			return nil, err
		}
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || definition.Query().VisibilitySnapshot
	}
	sources, err := e.prepareQuerySourcesLocked(scope, leaves, len(leaves) == 0, invoker, plan.RowMark{})
	if err != nil {
		return nil, err
	}
	return &storedSourcePreparation{root: root, definition: definition, consumer: consumer, sources: sources}, nil
}

func (e *Engine) captureStoredSourceLocked(scope *StatementScope, prepared *storedSourcePreparation, locks *exec.LockContext) (*exec.Source, error) {
	ctx := scope.op.Context()
	definition, consumer, leaves := prepared.definition, prepared.consumer, prepared.sources.leaves
	source, err := e.capturePreparedQuerySourcesLocked(scope, prepared.sources, locks)
	e.mu.Unlock()
	defer e.mu.Lock()
	if err != nil {
		return nil, err
	}
	if err = exec.SelectQuerySources(ctx, source); err != nil {
		source.Close()
		return nil, err
	}
	// Detach bundle ownership before replacing execution-local occurrences.
	sources := source.Sources
	source.Sources = nil
	for i, leaf := range definition.Sources() {
		child := source
		if sources != nil {
			child = sources[i]
		}
		live := leaves[i].Relation.Attributes
		unchanged := len(leaf.Attributes) == len(live)
		if unchanged {
			for slot, attribute := range leaf.Attributes {
				if attribute != live[slot] {
					unchanged = false
					break
				}
			}
		}
		if !unchanged {
			layout := make([]int, len(leaf.Attributes))
			for slot, attribute := range leaf.Attributes {
				layout[slot] = -1
				for current, id := range live {
					if attribute == id {
						layout[slot] = current
						break
					}
				}
			}
			child = &exec.Source{Ref: leaves[i].Ref, Input: child, Layout: layout, Scratch: make([]sqltype.Value, len(layout)), Authority: scope.authority, Locks: child.Locks, Occurrence: child.Occurrence}
			borrowQueryAuthority(child, scope)
		}
		if leaf.Ref != leaves[i].Ref {
			child.StoredRef = leaf.Ref
		}
		if sources != nil {
			sources[i] = child
		} else {
			source = child
		}
	}
	if sources != nil {
		source = sources[0]
		source.Sources = sources
	}
	borrowQueryAuthority(source, scope)
	// Only this opaque stored tree borrows its defining recipe. User/grafted
	// replacements retain their existing per-node ancestry rules.
	source.RoutineRecipe = consumer
	if recipe := stored.ViewOf(consumer); recipe != nil {
		source.RoutineChecks = recipe.Dependencies()
	}
	return source, nil
}
