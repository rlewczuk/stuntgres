package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func querySetNode(node LogicalNode) *setNode {
	switch n := node.(type) {
	case *setNode:
		return n
	case *recursiveUnionNode:
		return &n.setNode
	}
	return nil
}

// Generation buffers contain indexes into immutable accepted output tuples.
// Neither a pass nor a worktable reader copies the accumulated tuple payload.
type recursiveUnionStage struct {
	state          queryState
	node           *recursiveUnionNode
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

func recursiveCTEOwner(state *queryState, d *cteDefinition) *cteInvocation {
	for owner := state.ctes; owner != nil; owner = owner.parent {
		if owner.frame == d.Owner {
			return owner
		}
	}
	return nil
}

func openRecursiveUnion(ctx context.Context, state *queryState, n *recursiveUnionNode) (indexedStage, error) {
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

func openWorktableScan(ctx context.Context, state *queryState, n *worktableScanNode) (indexedStage, error) {
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
