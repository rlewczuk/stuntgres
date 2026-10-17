package engine

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"stuntgres/internal/sqltype"
)

// Positive permission belongs only to the eager top-level SELECT entry. Generic
// finalization remains serial for writes, cursors, nested queries and controls.
func finalizeSelectQuery(ctx context.Context, root LogicalNode, source *querySource, parameters []sqltype.Value, ceiling int) (RowResult, int, error) {
	project, ordered, eligible := parallelQueryPlan(root, source)
	workers := queryParallelWorkers(ceiling, source.count())
	if !eligible || workers < 2 {
		return finalizeQuery(ctx, root, source, parameters)
	}
	return finalizeParallelQuery(ctx, project, ordered, source, parameters, workers)
}

func queryParallelWorkers(ceiling, count int) int {
	return min(ceiling, count, max(2, runtime.GOMAXPROCS(0)))
}

func parallelQueryPlan(root LogicalNode, source *querySource) (*projectNode, *sortNode, bool) {
	project, ok := root.(*projectNode)
	if !ok || source == nil || source.view == nil || project.cteOwner || project.hasTargetSet || project.frozenValues || source.sources != nil || source.storedRef != (ObjectRef{}) ||
		source.locks != nil || source.lineage != nil || source.inherited.mode != 0 || source.hashIndexes != nil || source.partialMask != nil || source.inheritance != nil ||
		source.singleton || source.rlsDenied || source.anonymous != nil || source.pgSequences != nil || source.materialized || source.final != nil || source.input != nil ||
		source.nestedRoot != nil || source.nestedInputs != nil || source.nestedStage != nil || source.window || source.joinLeft != nil || source.joinRight != nil ||
		source.scratch != nil || source.layout != nil || source.versions != nil || source.invocationLineage != nil {
		return nil, nil, false
	}
	catalog := source.view.Catalog()
	d, ok := catalog.Descriptor(source.ref.Address)
	relation, table := d.(*relationDescriptor)
	if !ok || !table || relation.relationKind != tableRelation {
		return nil, nil, false
	}
	ns, ok := catalog.Descriptor(relation.namespace)
	namespace, namespaceOK := ns.(*namespaceDescriptor)
	if !ok || !namespaceOK || namespace.temporary != (SessionToken{}) {
		return nil, nil, false
	}
	frame := project.frame
	if frame == 0 {
		frame = 1
	}
	safe := func(expr BoundExpr) bool { return parallelQueryExpression(expr, frame, len(project.child.Schema())) }
	for _, expr := range project.expressions {
		if !safe(expr) {
			return nil, nil, false
		}
	}
	for _, expr := range project.analysisExpressions {
		if !safe(expr) {
			return nil, nil, false
		}
	}
	node := project.child
	var ordered *sortNode
	if sort, ok := node.(*sortNode); ok {
		if sort.presenceOnly {
			return nil, nil, false
		}
		ordered, node = sort, sort.child
		for _, key := range sort.keys {
			if !safe(key.expression) {
				return nil, nil, false
			}
		}
		for _, expr := range sort.outputs {
			if !safe(expr) {
				return nil, nil, false
			}
		}
	}
	if filter, ok := node.(*filterNode); ok {
		if !safe(filter.predicate) {
			return nil, nil, false
		}
		node = filter.child
	}
	leaf, ok := node.(*sourceNode)
	if !ok || leaf.ref != source.ref || leaf.occurrence != source.occurrence {
		return nil, nil, false
	}
	return project, ordered, true
}

// This is deliberately not a volatility classifier. Unknown evaluators, virtual
// recipes, subqueries and session/sequence functions fall back to serial. Basic
// scalar providers below operate on immutable datums and worker-local frames.
func parallelQueryExpression(expr BoundExpr, frame uint32, width int) bool {
	if expr == nil {
		return true
	}
	switch e := expr.(type) {
	case literalExpression, parameterExpression, cachedOrderExpression:
		return true
	case columnExpression:
		return e.virtual == nil && !e.mergeSource && e.scope == 1 && e.frame == frame && e.slot >= 0 && e.slot < width
	case coercionExpression:
		if !parallelScalarType(e.ResultUsage().Type) || !parallelScalarType(e.child.ResultUsage().Type) {
			return false
		}
	case selectedOperatorExpression, arithmeticExpression, comparisonExpression, logicalExpression, distinctExpression, nullTestExpression, truthTestExpression, caseExpression, collationExpression:
	default:
		return false
	}
	if expr.Properties().Volatility == Volatile {
		return false
	}
	safe := true
	boundExpressionChildren(expr, func(child BoundExpr) { safe = safe && parallelQueryExpression(child, frame, width) })
	return safe
}

func parallelScalarType(t *sqltype.Descriptor) bool {
	if t == nil {
		return false
	}
	switch t.ID() {
	case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Text, sqltype.Bool:
		return true
	default:
		return false
	}
}

// A worker gets an independent release handle and only audited immutable source
// fields. In particular no scratch, presence, ownership maps or child graph is
// shallow-copied from the admitted source.
func parallelQuerySource(source *querySource) (*querySource, error) {
	pin := source.view.Retain()
	if pin == nil {
		return nil, fmt.Errorf("stuntgres: visibility view is released")
	}
	return &querySource{view: pin, ref: source.ref, committed: source.committed, own: source.own, visible: source.visible,
		needsBirthFilter: source.needsBirthFilter, occurrence: source.occurrence, authority: source.authority, scope: source.scope}, nil
}

type queryParallelRun struct {
	indices     []int
	values      []sqltype.Value
	orderValues []sqltype.Value
	permutation []int
	cacheWidth  int
	count       int
	start       int // Allocation-free contiguous run when indices is nil.
}

func (r *queryParallelRun) index(ordinal int) int {
	if r.permutation != nil {
		ordinal = r.permutation[ordinal]
	}
	if r.indices != nil {
		return r.indices[ordinal]
	}
	return r.start + ordinal
}

func (r *queryParallelRun) sortRow(source *querySource, ordinal int) querySortRow {
	row := querySortRow{source: source, index: r.index(ordinal)}
	if r.cacheWidth != 0 {
		cache := ordinal
		if r.permutation != nil {
			cache = r.permutation[ordinal]
		}
		row.values = r.orderValues[cache*r.cacheWidth : (cache+1)*r.cacheWidth]
	}
	return row
}

func parallelProjectionSlots(project *projectNode) ([]int, bool) {
	for _, expr := range project.expressions {
		column, ok := expr.(columnExpression)
		if !ok || column.virtual != nil {
			return nil, false
		}
	}
	slots := make([]int, len(project.expressions))
	for i, expr := range project.expressions {
		slots[i] = expr.(columnExpression).slot
	}
	return slots, true
}

func runParallelQuery(ctx context.Context, project *projectNode, source *querySource, parameters []sqltype.Value, start, end int, input sqltype.InputEnvironment, output sqltype.OutputEnvironment, direct bool) (run queryParallelRun, err error) {
	worker, err := parallelQuerySource(source)
	if err != nil {
		return run, err
	}
	defer worker.close()
	state := &queryState{source: worker, parameters: parameters, input: input, output: output, executionContext: ctx, scanRange: true, scanStart: start, scanEnd: end}
	defer state.releaseMemberships()
	stage, err := openQueryProjectStage(ctx, state, project)
	if err != nil {
		return run, err
	}
	defer func() {
		if closeErr := stage.Close(); err == nil {
			err = closeErr
		}
	}()
	if err = stage.drain(ctx); err != nil {
		return run, err
	}
	run.count = stage.count()
	width := len(project.Schema())
	if !direct {
		run.values = make([]sqltype.Value, run.count*width)
	}
	for i := range run.count {
		var dst []sqltype.Value
		if !direct {
			dst = run.values[i*width : (i+1)*width]
		}
		var present bool
		present, err = stage.projectOrdinal(ctx, i, dst)
		if err != nil {
			return queryParallelRun{}, err
		}
		if !present {
			return queryParallelRun{}, fmt.Errorf("stuntgres: early parallel projection EOF")
		}
	}
	// Reuse sorted physical selections when possible; cache permutations retain
	// their separate sort-key identity until the leader finishes the real merge.
	run.indices, run.start = stage.selection, stage.scanStart
	run.orderValues, run.permutation, run.cacheWidth = stage.orderValues, stage.permutation, stage.cacheWidth
	return run, ctx.Err()
}

func finalizeParallelQuery(ctx context.Context, project *projectNode, ordered *sortNode, source *querySource, parameters []sqltype.Value, workers int) (RowResult, int, error) {
	keep := false
	defer func() {
		if !keep {
			source.close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	input, output := sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}
	if scope := source.liveScope(); scope != nil {
		input, output = scope.input, scope.output
	}
	slots, direct := parallelProjectionSlots(project)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runs := make([]queryParallelRun, workers)
	var joined sync.WaitGroup
	var errorMu sync.Mutex
	var first error
	n := source.count()
	for i := range workers {
		joined.Add(1)
		go func(i int) {
			defer joined.Done()
			run, err := runParallelQuery(workerCtx, project, source, parameters, i*n/workers, (i+1)*n/workers, input, output, direct)
			if err != nil {
				errorMu.Lock()
				if first == nil {
					first = err
					cancel()
				}
				errorMu.Unlock()
				return
			}
			runs[i] = run
		}(i)
	}
	joined.Wait() // No authority or input pin can outlive statement finalization.
	if first != nil {
		return nil, 0, first
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	result, err := gatherQueryRuns(ctx, project, ordered, source, runs, slots, direct, input, output)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		result.Close()
		return nil, 0, err
	}
	if direct && result.count > 0 {
		keep = true
		detachQuerySource(source)
	}
	return result, result.count, nil
}

// Gather concatenates occurrences without an SQL order promise. Gather Merge
// selects sorted run heads with the exact same fallible comparator as serial
// sort. It never concatenates then globally sorts and never evaluates targets.
func gatherQueryRuns(ctx context.Context, project *projectNode, ordered *sortNode, source *querySource, runs []queryParallelRun, slots []int, direct bool, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (*finalRows, error) {
	count := 0
	for i := range runs {
		count += runs[i].count
	}
	r := newFinalRows(project.Schema(), count)
	r.output = output
	width := len(r.schema)
	if direct {
		r.selection, r.slots = make([]int, count), slots
	} else {
		r.values = make([]sqltype.Value, count*width)
	}
	if ordered == nil {
		position := 0
		for i := range runs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			next := position + runs[i].count
			if direct {
				if runs[i].indices != nil {
					copy(r.selection[position:next], runs[i].indices)
				} else {
					for j := range runs[i].count {
						r.selection[position+j] = runs[i].index(j)
					}
				}
			} else {
				copy(r.values[position*width:next*width], runs[i].values)
			}
			position = next
		}
	} else {
		positions := make([]int, len(runs))
		for i := range count {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			best := -1
			for j := range runs {
				if positions[j] == runs[j].count {
					continue
				}
				if best < 0 {
					best = j
					continue
				}
				sign, err := compareQueryOrder(ctx, ordered.keys, input, runs[j].sortRow(source, positions[j]), runs[best].sortRow(source, positions[best]))
				if err != nil {
					return nil, err
				}
				if sign < 0 {
					best = j
				}
			}
			if best < 0 {
				return nil, fmt.Errorf("stuntgres: early parallel merge EOF")
			}
			position := positions[best]
			if direct {
				r.selection[i] = runs[best].index(position)
			} else {
				copy(r.values[i*width:(i+1)*width], runs[best].values[position*width:(position+1)*width])
			}
			positions[best]++
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if direct && count > 0 {
		r.source = source
	}
	return r, nil
}

type querySortRow struct {
	source *querySource
	index  int
	values []sqltype.Value
}

func (r querySortRow) value(key sortKey) sqltype.Value {
	if key.cache >= 0 {
		return r.values[key.cache]
	}
	return r.source.row(r.index)[key.slot]
}

func compareQueryOrder(ctx context.Context, keys []sortKey, input sqltype.InputEnvironment, a, b querySortRow) (int, error) {
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// Preserve borrowed-source ordering: capture left before asking for right.
		left, right := a.value(key), b.value(key)
		if left.IsNull() && right.IsNull() {
			continue
		}
		if left.IsNull() || right.IsNull() {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if left.IsNull() == key.nullsFirst {
				return -1, nil
			}
			return 1, nil
		}
		sign, err := key.comparison.Compare(ctx, left, right, input)
		if err != nil {
			return 0, err
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if sign != 0 {
			if key.desc {
				sign = -sign
			}
			return sign, nil
		}
	}
	return 0, ctx.Err()
}
