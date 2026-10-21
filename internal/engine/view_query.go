package engine

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// prepareStoredQueryLocked consumes Engine.mu on all returns. Both ordinary
// views and refresh execute the frozen tree through current attribute bridges.
func (e *Engine) prepareStoredQueryLocked(scope *StatementScope, ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, locks *exec.LockContext, outputUses []int) (plan.Node, *exec.Source, error) {
	ctx := scope.op.Context()
	if scope.authority == nil {
		scope.authority = &exec.Authority{Host: hostOf(scope)}
	}
	auth, err := e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark)
	if err != nil {
		e.mu.Unlock()
		return nil, nil, err
	}
	catalog := e.privateCatalogLocked(scope.owner.Catalog)
	e.mu.Unlock()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope), Catalog: catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	evaluation.QuoteCaptures = scope.quoteCaptures
	evaluation.QuoteOrigin = exec.QuoteOrigin{Owner: ref, Definition: definition.RuleOID(), Revision: definition.QuoteRevision()}
	consumer, ok := catalog.Relation(ref.Address)
	if !ok || stored.ViewOf(consumer) != definition {
		return nil, nil, ids.ErrCatalogReplaced
	}
	evaluation.RoutineRecipe, evaluation.RoutineConsumer = true, consumer
	evaluation.RoutineChecks = definition.Dependencies()
	evaluation.RoutineAuthority = &auth
	root, reads, err := prepareStoredExecutionRoot(&evaluation, definition, outputUses)
	if err == nil {
		err = validateMarkedQueryJoin(&evaluation, root, catalog, nil, plan.RowMark{}, false, nil, reads)
	}
	if err != nil {
		return nil, nil, err
	}
	e.mu.Lock()
	if auth, err = e.authorizeViewSourceLocked(scope, ref, definition, invoker, mark); err != nil {
		e.mu.Unlock()
		return nil, nil, err
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
			e.mu.Unlock()
			return nil, nil, err
		}
		leaves[i] = exec.Leaf{Ref: use, Relation: relation, Only: leaf.Only, Principal: relationPrincipal, Storage: e.privateStorageLocked(scope.owner.Catalog, leaf.Ref.Address), Mark: viewLeafRowMark(definition, i, mark), AnalysisOnly: leaf.AnalysisOnly}
		leaves[i].Uses, err = analyze.QuerySourceReadUses(reads[i], relation.Attributes)
		if err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
	}
	if scope.materializedOrZero().Refresh && ref == scope.lockedRef {
		if err := e.initializeQueryLocksLocked(scope, root, leaves); err != nil {
			e.mu.Unlock()
			return nil, nil, err
		}
		scope.inquiry.visibilityDemand = scope.inquiry.visibilityDemand || definition.Query().VisibilitySnapshot
		locks = scope.queryLocks
	}
	source, err := e.captureQuerySourcesForInvokerLocked(scope, leaves, len(leaves) == 0, invoker, plan.RowMark{}, locks)
	e.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	if err = exec.SelectQuerySources(ctx, source); err != nil {
		source.Close()
		return nil, nil, err
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
	return root, source, nil
}

// Legality and source opening consume the same execution-owned projection and
// scheduled producers. Frozen authorization reads and dependencies are unchanged.
func prepareStoredExecutionRoot(c *exec.Context, definition *stored.View, outputUses []int) (plan.Node, map[int][]int32, error) {
	demanded := definition.Query().Root
	if project, ok := demanded.(*plan.ProjectNode); ok {
		var err error
		demanded, err = StoredProjectionDemand(project, outputUses)
		if err != nil {
			return nil, nil, err
		}
	} else {
		for _, slot := range outputUses {
			if slot < 0 || slot >= len(demanded.Schema()) {
				return nil, nil, fmt.Errorf("stuntgres: invalid stored projection use")
			}
		}
	}
	root, err := preflightQuery(c, demanded)
	if err != nil {
		return nil, nil, err
	}
	reads := analyze.QueryExecutionSourceReads(root, nil)
	if source, ok := root.(*plan.SourceNode); ok {
		if source.Occurrence < 0 || source.Occurrence >= len(definition.Sources()) {
			return nil, nil, fmt.Errorf("stuntgres: invalid stored source occurrence")
		}
		attributes := definition.Sources()[source.Occurrence].Attributes
		if outputUses == nil {
			reads[source.Occurrence] = attributes
		} else {
			items := make([]int32, 0, len(outputUses))
			for _, slot := range outputUses {
				if slot >= len(attributes) {
					return nil, nil, fmt.Errorf("stuntgres: invalid stored source attribute")
				}
				found := false
				for _, attribute := range items {
					found = found || attribute == attributes[slot]
				}
				if !found {
					items = append(items, attributes[slot])
				}
			}
			reads[source.Occurrence] = items
		}
	}
	return root, reads, nil
}
