package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// Rechecks share the ordinary JOIN/filter demand machinery, but each nonfunction
// subtree supplies only its originally captured occurrence with refreshed leaves.
// The execution-local cache is separate from the immutable retained blueprint.
type stringRecheckCache struct {
	topologies map[LogicalNode]*stringRecheckTopology
	functions  map[*stringTableNode][]stringRecheckCall
}
type stringRecheckCall struct {
	cursor           sqltype.StringFieldCursor
	ready, dependent bool
}

func (c *stringRecheckCache) close() {
	for _, calls := range c.functions {
		for i := range calls {
			calls[i].cursor.Close()
		}
	}
	c.functions = nil
	c.topologies = nil
}

type stringRecheckEntry struct {
	expanding bool
	frozen    []sqltype.Value
}
type stringRecheckTopology struct {
	entries map[LogicalNode]stringRecheckEntry
}
type stringQueryRecheck struct {
	topology   *stringRecheckTopology
	cache      *stringRecheckCache
	inputs     *querySource
	rows       [][]sqltype.Value
	evaluation EvalContext
}

func stringRecheckContains(node LogicalNode) bool {
	switch n := node.(type) {
	case *stringTableNode:
		return true
	case *derivedNode, *sourceNode, *setProjectNode, *aggregateNode, *windowNode, *distinctNode, *distinctOnNode, *setNode:
		return false
	case *projectNode:
		if n.frozenValues {
			return false
		}
	}
	left, right := queryInputs(node)
	return left != nil && stringRecheckContains(left) || right != nil && stringRecheckContains(right)
}
func (t *stringRecheckTopology) capture(node LogicalNode, frozen []sqltype.Value) error {
	entry, known := t.entries[node]
	if !known {
		entry.expanding = stringRecheckContains(node)
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
func recheckStringQuery(node LogicalNode, inputs *querySource, rows [][]sqltype.Value, evaluation *EvalContext, scratch []sqltype.Value, offset *int, frozen []sqltype.Value) (result []sqltype.Value, present bool, err error) {
	cache := evaluation.stringRecheckCache
	if cache == nil {
		cache = &stringRecheckCache{}
		defer cache.close()
	}
	if cache.topologies == nil {
		cache.topologies = make(map[LogicalNode]*stringRecheckTopology)
	}
	topology := cache.topologies[node]
	if topology == nil {
		topology = &stringRecheckTopology{entries: make(map[LogicalNode]stringRecheckEntry)}
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
	state.stringRecheck = &stringQueryRecheck{topology: topology, cache: cache, inputs: inputs, rows: rows, evaluation: *evaluation}
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
func openStringRecheckPoint(ctx context.Context, state *queryState, node LogicalNode) (indexedStage, error) {
	recheck := state.stringRecheck
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
func (c *stringRecheckCache) open(evaluation *EvalContext, node *stringTableNode) (stringSetInvocation, error) {
	if c.functions == nil {
		c.functions = make(map[*stringTableNode][]stringRecheckCall)
	}
	cached := c.functions[node]
	if cached == nil {
		cached = make([]stringRecheckCall, len(node.calls))
		for i, call := range node.calls {
			for _, arg := range call.args {
				cached[i].dependent = cached[i].dependent || stringFrameReference(arg, node.frame)
			}
		}
		c.functions[node] = cached
	}
	invocation := stringSetInvocation{cursors: make([]stringSetCursor, len(node.calls))}
	for i, call := range node.calls {
		if !cached[i].dependent && cached[i].ready {
			invocation.cursors[i].cursor = cached[i].cursor
			invocation.cursors[i].initialized = true
			continue
		}
		cursor, err := openStringCall(evaluation, call)
		if err != nil {
			invocation.close()
			return stringSetInvocation{}, err
		}
		if !cached[i].dependent {
			cached[i].cursor = cursor
			cached[i].ready = true
		}
		invocation.cursors[i].cursor = cursor
		invocation.cursors[i].initialized = true
	}
	return invocation, nil
}
