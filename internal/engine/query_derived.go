package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// A derived source adapts projected child rows to the ordinary FROM frame.
// Its output is execution-private; it never owns captured relation pins.
type derivedNode struct {
	querySchema
	query        *BoundQuery
	cte          *cteDefinition
	table        *parser.TableReference
	ordinal      int
	scanFrame    uint32
	demand       []bool
	presenceSlot int
}

func openDerivedNode(ctx context.Context, state *queryState, n *derivedNode) (indexedStage, error) {
	if n.cte != nil && !n.cte.folded() {
		return openCTEScan(ctx, state, n)
	}
	project, ok := n.query.Root.(*projectNode)
	if !ok {
		return indexedStage{}, fmt.Errorf("stuntgres: derived query lacks final projection")
	}
	innerState := &queryState{source: state.source, parameters: state.parameters, input: state.input, output: state.output, locking: state.locking, outer: state.outer, invocation: state.invocation, foreignOwner: state.foreignOwner, ctes: state.ctes, retain: state.retain, hashOwner: state.membershipOwner()}
	inner, err := openQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return indexedStage{}, err
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope, locks: state.source.locks}
	establishSourcePresence(source, n.presenceSlot)
	if state.source.locks != nil {
		source.lineage = &queryLockLineage{root: project, inputs: state.source, stage: inner.source, state: innerState}
	}
	if state.derived == nil {
		state.derived = make(map[*derivedNode]*querySource)
	}
	state.derived[n] = source
	out := indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	out.reset = func(ctx context.Context, out *indexedStage) (bool, error) {
		position := inner.position
		changed, err := inner.rewind(ctx)
		if err != nil {
			return false, err
		}
		if !changed {
			// Kept derived captures must resume at the uncaptured child occurrence,
			// not append the cached prefix again after a parent rewind.
			inner.position = position
			return false, nil
		}
		source.flat, source.flatCount = source.flat[:0], 0
		out.selection, out.done = out.selection[:0], false
		if source.lineage != nil {
			source.lineage.selection = source.lineage.selection[:0]
			source.lineage.cacheOccurrences = source.lineage.cacheOccurrences[:0]
		}
		return true, nil
	}
	out.finish = func() error {
		err := inner.Close()
		delete(state.derived, n)
		return err
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		i, present, err := inner.nextOccurrence(ctx)
		if err != nil || !present {
			return present, err
		}
		inner.evaluation.setContext(ctx)
		inner.evaluation.bindQueryRow(inner.source, inner.index(i))
		inner.evaluation.orderValues = inner.orderRow(i)
		start := len(source.flat)
		source.flat = append(source.flat, make([]sqltype.Value, len(n.Schema()))...)
		row := source.flat[start:]
		for j, expr := range inner.expressions {
			if n.cte != nil && !n.demand[j] {
				row[j] = sqltype.ResultNull(n.Schema()[j].Usage)
				continue
			}
			value, err := expr.Eval(&inner.evaluation)
			if err != nil {
				return false, err
			}
			if err := validateProducedValue(value, n.Schema()[j].Usage); err != nil {
				return false, err
			}
			row[j] = value
		}
		if source.lineage != nil {
			lineage := source.lineage
			lineage.stage = inner.source
			lineage.selection = append(lineage.selection, inner.index(i))
			lineage.cacheWidth, lineage.orderValues = inner.cacheWidth, inner.orderValues
			if inner.cacheWidth != 0 {
				lineage.cacheOccurrences = append(lineage.cacheOccurrences, inner.orderOccurrence(i))
			}
		}
		out.selection = append(out.selection, source.flatCount)
		source.flatCount++
		return true, ctx.Err()
	}
	return out, nil
}
