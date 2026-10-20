package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (s *queryState) inheritCTEs() {
	if s.ctes == nil && s.source != nil {
		if host := s.source.liveHost(); host != nil {
			s.ctes = host.CTEInvocation()
		}
	}
}

// Generation buffers contain indexes into immutable accepted output tuples.
// Neither a pass nor a worktable reader copies the accumulated tuple payload.
type recursiveUnionStage struct {
	state          queryState
	node           *plan.RecursiveUnionNode
	source         *querySource
	current, next  []int
	seen           map[uint64][]int
	arm            setArm
	scratch        []sqltype.Value
	mark, fallback sqltype.Value
	marksReady     bool
	phase          uint8
	opened, closed bool
}

func recursiveCTEOwner(state *queryState, d *plan.CTEDefinition) *cteInvocation {
	for owner := state.ctes; owner != nil; owner = owner.parent {
		if owner.frame == d.Owner {
			return owner
		}
	}
	return nil
}

func openRecursiveUnion(ctx context.Context, state *queryState, n *plan.RecursiveUnionNode) (indexedStage, error) {
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	owner := recursiveCTEOwner(state, n.Definition)
	if owner == nil || owner.closed || owner.producers[n.Definition] == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing recursive CTE producer")
	}
	producer := owner.producers[n.Definition]
	stage := &recursiveUnionStage{state: *state, node: n, source: producer.source}
	if !n.All && len(n.Schema()) > 0 {
		stage.seen = make(map[uint64][]int)
	}
	producer.recursion = stage
	return indexedStage{source: stage.source, schema: n.Schema(), selection: []int{}, produce: stage.produce, finish: stage.close}, nil
}

func (s *recursiveUnionStage) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.current, s.next, s.seen = nil, nil, nil
	s.scratch = nil
	if s.opened {
		s.opened = false
		return s.arm.Close()
	}
	return nil
}

func (s *recursiveUnionStage) produce(ctx context.Context, out *indexedStage) (bool, error) {
	for s.phase < 2 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !s.opened {
			project, expressions := s.node.Left, s.node.LeftExpressions
			if s.phase == 1 {
				project, expressions = s.node.Right, s.node.RightExpressions
			}
			arm, err := openSetArmScratch(ctx, &s.state, project, expressions, s.node.Schema(), s.scratch)
			if err != nil {
				return false, err
			}
			s.arm, s.opened = arm, true
			s.scratch = arm.row
			if c := s.node.Cycle; c != nil && !s.marksReady {
				evaluation := &s.arm.inner.evaluation
				evaluation.setContext(ctx)
				s.mark, err = c.Mark.Eval(evaluation)
				if err != nil {
					return false, err
				}
				s.fallback, err = c.Fallback.Eval(evaluation)
				if err != nil {
					return false, err
				}
				s.marksReady = true
			}
		}
		row, present, err := s.arm.next(ctx)
		if err != nil {
			return false, err
		}
		if !present {
			s.opened = false
			if err := s.arm.Close(); err != nil {
				return false, err
			}
			if s.phase == 0 {
				s.phase = 1
			} else {
				s.current, s.next = s.next, s.current[:0]
			}
			if len(s.current) == 0 {
				s.phase = 2
				return false, nil
			}
			continue
		}
		cycle, search := s.node.Cycle, s.node.Search
		var parentSearch, parentCycle sqltype.Value
		if s.phase == 1 {
			if search != nil {
				parentSearch = row[search.Slot]
			}
			if cycle != nil {
				parentCycle = row[cycle.MarkSlot+1]
			}
		}
		if search != nil {
			value, failure := search.Value(ctx, row, parentSearch)
			if failure != nil {
				return false, failure
			}
			row[search.Slot] = value
		}
		if cycle != nil {
			row, err = cycle.Augment(ctx, row, parentCycle, s.mark, s.fallback)
			if err != nil {
				return false, err
			}
		}
		hash := uint64(14695981039346656037)
		if s.seen != nil {
			for i, family := range s.node.Grouping {
				slot := s.node.GroupingSlot(i)
				value, err := family.PrepareKey(ctx, row[slot])
				if err != nil {
					return false, err
				}
				row[slot] = value
				part, err := family.Hash(ctx, value)
				if err != nil {
					return false, err
				}
				hash = (hash ^ part) * 1099511628211
			}
			if search != nil {
				part, err := search.Hash(ctx, row[search.Slot])
				if err != nil {
					return false, err
				}
				hash = (hash ^ part) * 1099511628211
			}
			if cycle != nil {
				part, err := cycle.HashPath(ctx, row[cycle.MarkSlot+1])
				if err != nil {
					return false, err
				}
				hash = (hash ^ part) * 1099511628211
			}
			index, err := s.findTuple(ctx, row, s.seen[hash])
			if err != nil {
				return false, err
			}
			if index >= 0 {
				continue
			}
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		index := s.source.flatCount
		s.source.flat = append(s.source.flat, row...)
		s.source.flatCount++
		if s.seen != nil {
			s.seen[hash] = append(s.seen[hash], index)
		}
		expand := true
		if cycle != nil {
			value, err := cycle.Inequality.Evaluate(ctx, row[cycle.MarkSlot], s.mark, s.state.input)
			if err != nil {
				return false, err
			}
			expand = false
			if !value.IsNull() {
				expand, err = value.Boolean()
				if err != nil {
					return false, err
				}
			}
		}
		if expand {
			if s.phase == 0 {
				s.current = append(s.current, index)
			} else {
				s.next = append(s.next, index)
			}
		}
		out.selection = append(out.selection, index)
		return true, nil
	}
	return false, ctx.Err()
}

func openWorktableScan(ctx context.Context, state *queryState, n *plan.WorktableScanNode) (indexedStage, error) {
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	owner := recursiveCTEOwner(state, n.CTE)
	if owner == nil || owner.closed {
		return indexedStage{}, fmt.Errorf("stuntgres: missing recursive worktable owner")
	}
	producer := owner.producers[n.CTE]
	if producer == nil || producer.recursion == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing current recursive generation")
	}
	stage := producer.recursion
	source := stage.source
	if n.PresenceSlot != 0 {
		borrowed := *source
		source = &borrowed
		establishSourcePresence(source, n.PresenceSlot)
	}
	return indexedStage{source: source, schema: n.Schema(), selection: stage.current}, nil
}

func (s *recursiveUnionStage) findTuple(ctx context.Context, row []sqltype.Value, candidates []int) (int, error) {
	c, search := s.node.Cycle, s.node.Search
	if c == nil && search == nil {
		return findDistinctTuple(ctx, s.node.Grouping, row, s.source.flat, candidates)
	}
	width := len(s.node.Schema())
	for _, index := range candidates {
		other := s.source.flat[index*width : (index+1)*width]
		equal := true
		for i, g := range s.node.Grouping {
			slot := s.node.GroupingSlot(i)
			same, err := g.Equal(ctx, row[slot], other[slot])
			if err != nil {
				return -1, err
			}
			if !same {
				equal = false
				break
			}
		}
		if equal && search != nil {
			same, err := search.Equal(ctx, row[search.Slot], other[search.Slot])
			if err != nil {
				return -1, err
			}
			equal = same
		}
		if equal && c != nil {
			same, err := c.EqualPath(ctx, row[c.MarkSlot+1], other[c.MarkSlot+1])
			if err != nil {
				return -1, err
			}
			equal = same
		}
		if equal {
			return index, nil
		}
	}
	return -1, ctx.Err()
}

func setMultiplicity(operation string, all bool, left, right int) int {
	switch operation {
	case "union":
		if all {
			return left + right
		}
		if left+right != 0 {
			return 1
		}
	case "intersect":
		if all {
			return min(left, right)
		}
		if left != 0 && right != 0 {
			return 1
		}
	case "except":
		if all {
			return max(left-right, 0)
		}
		if left != 0 && right == 0 {
			return 1
		}
	}
	return 0
}

type setArm struct {
	inner       indexedStage
	expressions []expr.Expr
	schema      []catalog.Column
	row         []sqltype.Value
}

func openSetArm(ctx context.Context, state *queryState, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column) (setArm, error) {
	return openSetArmScratch(ctx, state, project, expressions, schema, nil)
}

func openSetArmScratch(ctx context.Context, state *queryState, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column, scratch []sqltype.Value) (setArm, error) {
	innerState := &queryState{source: state.source, parameters: state.parameters, input: state.input, output: state.output, locking: state.locking, outer: state.outer, ctes: state.ctes, hashOwner: state.membershipOwner()}
	inner, err := openQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return setArm{}, err
	}
	if scratch == nil {
		scratch = make([]sqltype.Value, len(schema))
	}
	return setArm{inner: inner, expressions: expressions, schema: schema, row: scratch}, nil
}

func (a *setArm) Close() error { return a.inner.Close() }

// The arm scratch is reused on every pull; only unique keys or demanded UNION
// ALL outputs are retained by the parent. Arm-local clauses execute first.
func (a *setArm) next(ctx context.Context) ([]sqltype.Value, bool, error) {
	i, present, err := a.inner.nextOccurrence(ctx)
	if err != nil || !present {
		return nil, present, err
	}
	a.inner.evaluation.setContext(ctx)
	a.inner.evaluation.bindQueryRow(a.inner.source, a.inner.index(i))
	a.inner.evaluation.orderValues = a.inner.orderRow(i)
	for j, expression := range a.expressions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		value, err := expression.Eval(&a.inner.evaluation)
		if err != nil {
			return nil, false, err
		}
		if err := validateProducedValue(value, a.schema[j].Usage); err != nil {
			return nil, false, err
		}
		a.row[j] = value
	}
	return a.row, true, ctx.Err()
}

func openSetNode(ctx context.Context, state *queryState, n *plan.SetNode) (out indexedStage, err error) {
	if n.Operation == "union" && n.All {
		return openUnionAll(ctx, state, n)
	}
	if len(n.Grouping) != len(n.Schema()) {
		return out, fmt.Errorf("stuntgres: invalid set grouping schema")
	}
	source := &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, host: state.source.host}
	buckets := make(map[uint64][]int)
	var counts [][2]int
	for side, arm := range [2]*plan.ProjectNode{n.Left, n.Right} {
		expressions := n.LeftExpressions
		if side == 1 {
			expressions = n.RightExpressions
		}
		child, failure := openSetArm(ctx, state, arm, expressions, n.Schema())
		if failure != nil {
			return indexedStage{}, failure
		}
		failure = func() (failure error) {
			defer func() {
				if closeErr := child.Close(); failure == nil {
					failure = closeErr
				}
			}()
			for {
				row, present, err := child.next(ctx)
				if err != nil {
					return err
				}
				if !present {
					break
				}
				hash := uint64(14695981039346656037)
				for j, family := range n.Grouping {
					value, err := family.PrepareKey(ctx, row[j])
					if err != nil {
						return err
					}
					row[j] = value
					h, err := family.Hash(ctx, value)
					if err != nil {
						return err
					}
					hash = (hash ^ h) * 1099511628211
				}
				index, err := findDistinctTuple(ctx, n.Grouping, row, source.flat, buckets[hash])
				if err != nil {
					return err
				}
				if index < 0 {
					index = source.flatCount
					buckets[hash] = append(buckets[hash], index)
					source.flat = append(source.flat, row...)
					source.flatCount++
					counts = append(counts, [2]int{})
				}
				counts[index][side]++
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			return ctx.Err()
		}()
		if failure != nil {
			return indexedStage{}, failure
		}
	}
	out = indexedStage{source: source, schema: n.Schema(), selection: []int{}}
	for i, count := range counts {
		for copies := setMultiplicity(n.Operation, n.All, count[0], count[1]); copies > 0; copies-- {
			if err := ctx.Err(); err != nil {
				return indexedStage{}, err
			}
			out.selection = append(out.selection, i)
		}
	}
	return out, ctx.Err()
}

func openUnionAll(ctx context.Context, state *queryState, n *plan.SetNode) (indexedStage, error) {
	out := indexedStage{source: &querySource{flatWidth: len(n.Schema()), materialized: true, authority: state.source.authority, host: state.source.host}, schema: n.Schema(), selection: []int{}}
	var child setArm
	opened, side := false, 0
	out.finish = func() error {
		if opened {
			opened = false
			return child.Close()
		}
		return nil
	}
	out.produce = func(ctx context.Context, out *indexedStage) (bool, error) {
		for side < 2 {
			if !opened {
				arm, expressions := n.Left, n.LeftExpressions
				if side == 1 {
					arm, expressions = n.Right, n.RightExpressions
				}
				var err error
				child, err = openSetArm(ctx, state, arm, expressions, n.Schema())
				if err != nil {
					return false, err
				}
				opened = true
			}
			row, present, err := child.next(ctx)
			if err != nil {
				return false, err
			}
			if !present {
				opened = false
				if err := child.Close(); err != nil {
					return false, err
				}
				side++
				continue
			}
			out.source.flat = append(out.source.flat, row...)
			out.selection = append(out.selection, out.source.flatCount)
			out.source.flatCount++
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out, nil
}
