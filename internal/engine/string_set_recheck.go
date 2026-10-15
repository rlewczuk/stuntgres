package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Rechecks share the ordinary JOIN/filter demand machinery, but each nonfunction
// subtree supplies only its originally captured occurrence with refreshed leaves.
// The execution-local cache is separate from the immutable retained blueprint.
type functionRecheckCache struct {
	topologies map[LogicalNode]*functionRecheckTopology
	functions  map[*functionFromNode][]functionRecheckCall
}
type functionRecheckCall struct {
	cursor           functionSetCursor
	ready, dependent bool
}

func (c *functionRecheckCache) close() {
	for _, calls := range c.functions {
		for i := range calls {
			calls[i].cursor.close()
		}
	}
	c.functions = nil
	c.topologies = nil
}

type functionRecheckEntry struct {
	expanding bool
	frozen    []sqltype.Value
}
type functionRecheckTopology struct {
	entries map[LogicalNode]functionRecheckEntry
}
type functionQueryRecheck struct {
	topology   *functionRecheckTopology
	cache      *functionRecheckCache
	inputs     *querySource
	rows       [][]sqltype.Value
	evaluation EvalContext
}

func functionRecheckContains(node LogicalNode) bool {
	switch n := node.(type) {
	case *functionFromNode:
		return true
	case *derivedNode, *sourceNode, *setProjectNode, *aggregateNode, *windowNode, *distinctNode, *distinctOnNode, *setNode:
		return false
	case *projectNode:
		if n.frozenValues {
			return false
		}
	}
	left, right := queryInputs(node)
	return left != nil && functionRecheckContains(left) || right != nil && functionRecheckContains(right)
}
func (t *functionRecheckTopology) capture(node LogicalNode, frozen []sqltype.Value) error {
	entry, known := t.entries[node]
	if !known {
		entry.expanding = functionRecheckContains(node)
	}
	entry.frozen = frozen
	t.entries[node] = entry
	if !entry.expanding {
		return nil
	}
	if n, ok := node.(*joinNode); ok {
		width := n.leftWidth + n.rightWidth
		if len(frozen) < width {
			return fmt.Errorf("stuntgres: missing captured function recheck frame")
		}
		if err := t.capture(n.left, frozen[:n.leftWidth]); err != nil {
			return err
		}
		return t.capture(n.right, frozen[n.leftWidth:width])
	}
	left, right := queryInputs(node)
	if left != nil {
		if err := t.capture(left, frozen); err != nil {
			return err
		}
	}
	if right != nil {
		return t.capture(right, frozen)
	}
	return nil
}
func recheckFunctionQuery(node LogicalNode, inputs *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int, frozen []sqltype.Value) (result []sqltype.Value, present bool, err error) {
	cache := evaluation.functionRecheckCache
	if cache == nil {
		cache = &functionRecheckCache{}
		defer cache.close()
	}
	if cache.topologies == nil {
		cache.topologies = make(map[LogicalNode]*functionRecheckTopology)
	}
	topology := cache.topologies[node]
	if topology == nil {
		topology = &functionRecheckTopology{entries: make(map[LogicalNode]functionRecheckEntry)}
		cache.topologies[node] = topology
	}
	if err := topology.capture(node, frozen); err != nil {
		return nil, false, err
	}
	state := queryState{source: inputs, parameters: evaluation.Parameters, input: evaluation.Input, output: evaluation.Output, frame: evaluation.Frame, outer: evaluation.Outer}
	if evaluation.query != nil {
		state = *evaluation.query
	}
	source := *state.source
	source.locks = nil
	source.scope = evaluation.liveScope()
	state.source = &source
	state.locking = false
	state.inherited = queryRowMark{}
	state.functionRecheck = &functionQueryRecheck{topology: topology, cache: cache, inputs: inputs, rows: rows, evaluation: *evaluation}
	stage, err := openQueryNode(evaluation.Context, &state, node)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if closeErr := stage.Close(); err == nil {
			err = closeErr
		}
	}()
	index, present, err := stage.nextOccurrence(evaluation.Context)
	if err != nil || !present {
		return nil, present, err
	}
	row := stage.source.row(stage.index(index))
	result = scratch[*offset : *offset+len(row)]
	*offset += len(row)
	copy(result, row)
	copy(evaluation.orderValues, stage.orderRow(index))
	return result, true, nil
}
func openFunctionRecheckPoint(ctx context.Context, state *queryState, node LogicalNode) (indexedStage, error) {
	recheck := state.functionRecheck
	entry, ok := recheck.topology.entries[node]
	if !ok {
		return indexedStage{}, fmt.Errorf("stuntgres: missing function recheck subtree")
	}
	evaluation := recheck.evaluation
	evaluation.setContext(ctx)
	scratch := make([]sqltype.Value, queryLockRecheckWidth(node, recheck.inputs))
	offset := 0
	row, present, err := recheckLockedQuery(node, recheck.inputs, recheck.rows, &evaluation, scratch, &offset, entry.frozen)
	if err != nil {
		return indexedStage{}, err
	}
	source := &querySource{flatWidth: len(node.Schema()), materialized: true, authority: state.source.authority, scope: state.source.scope}
	if present && !absentLockedQuery(node, recheck.inputs, evaluation.liveScope().queryLocks) {
		source.flat = row
		source.flatCount = 1
	}
	return indexedStage{source: source, schema: node.Schema()}, nil
}
func (c *functionRecheckCache) open(evaluation *EvalContext, node *functionFromNode) (functionSetInvocation, error) {
	if c.functions == nil {
		c.functions = make(map[*functionFromNode][]functionRecheckCall)
	}
	cached := c.functions[node]
	if cached == nil {
		cached = make([]functionRecheckCall, len(node.calls))
		for i, call := range node.calls {
			for _, arg := range functionCallArguments(call) {
				cached[i].dependent = cached[i].dependent || functionFrameReference(arg, node.frame)
			}
		}
		c.functions[node] = cached
	}
	invocation := functionSetInvocation{cursors: make([]functionSetCursor, len(node.calls))}
	for i := range node.calls {
		if !cached[i].dependent && cached[i].ready {
			invocation.cursors[i] = cached[i].cursor
			continue
		}
		cursor, err := node.openCall(evaluation, i)
		if err != nil {
			invocation.close()
			return functionSetInvocation{}, err
		}
		if !cached[i].dependent {
			cached[i].cursor = cursor
			cached[i].ready = true
		}
		invocation.cursors[i] = cursor
	}
	return invocation, nil
}
