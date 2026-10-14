package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// The self edge is an opaque identity, never a child pointing at its own query.
type worktableScanNode struct{ derivedNode }
type recursiveUnionNode struct {
	setNode
	definition *cteDefinition
}

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
	phase          uint8
	opened, closed bool
}

func recursiveCTEOwner(state *queryState, d *cteDefinition) *cteInvocation {
	for owner := state.ctes; owner != nil; owner = owner.parent {
		if owner.frame == d.owner {
			return owner
		}
	}
	return nil
}

func openRecursiveUnion(ctx context.Context, state *queryState, n *recursiveUnionNode) (indexedStage, error) {
	if err := ctx.Err(); err != nil {
		return indexedStage{}, err
	}
	owner := recursiveCTEOwner(state, n.definition)
	if owner == nil || owner.closed || owner.producers[n.definition] == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing recursive CTE producer")
	}
	producer := owner.producers[n.definition]
	stage := &recursiveUnionStage{state: *state, node: n, source: producer.source}
	if !n.all && len(n.Schema()) > 0 {
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
			project, expressions := s.node.left, s.node.leftExpressions
			if s.phase == 1 {
				project, expressions = s.node.right, s.node.rightExpressions
			}
			arm, err := openSetArmScratch(ctx, &s.state, project, expressions, s.node.Schema(), s.scratch)
			if err != nil {
				return false, err
			}
			s.arm, s.opened = arm, true
			s.scratch = arm.row
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
		hash := uint64(14695981039346656037)
		if s.seen != nil {
			for i, family := range s.node.grouping {
				value, err := family.PrepareKey(ctx, row[i])
				if err != nil {
					return false, err
				}
				row[i] = value
				part, err := family.Hash(ctx, value)
				if err != nil {
					return false, err
				}
				hash = (hash ^ part) * 1099511628211
			}
			index, err := findDistinctTuple(ctx, s.node.grouping, row, s.source.flat, s.seen[hash])
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
		if s.phase == 0 {
			s.current = append(s.current, index)
		} else {
			s.next = append(s.next, index)
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
	owner := recursiveCTEOwner(state, n.cte)
	if owner == nil || owner.closed {
		return indexedStage{}, fmt.Errorf("stuntgres: missing recursive worktable owner")
	}
	producer := owner.producers[n.cte]
	if producer == nil || producer.recursion == nil {
		return indexedStage{}, fmt.Errorf("stuntgres: missing current recursive generation")
	}
	stage := producer.recursion
	source := stage.source
	if n.presenceSlot != 0 {
		borrowed := *source
		source = &borrowed
		establishSourcePresence(source, n.presenceSlot)
	}
	return indexedStage{source: source, schema: n.Schema(), selection: stage.current}, nil
}
