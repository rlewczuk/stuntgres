package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

func (s *State) inheritCTEs() {
	// Partial workers already own the outer CTE snapshot selected before any
	// support body enters the shared SQL host. A captured nil is authoritative:
	// never inherit that host's temporarily installed/restored lexical scratch.
	if s.aggregatePartials != nil || s.ClosedEvaluation != nil {
		return
	}
	if s.Ctes == nil && s.Source != nil {
		if host := s.Source.LiveHost(); host != nil {
			s.Ctes = host.CTEInvocation()
		}
	}
}

// Generation buffers contain indexes into immutable accepted output tuples.
// Neither a pass nor a worktable reader copies the accumulated tuple payload.
type recursiveUnionStage struct {
	state          State
	Node           *plan.RecursiveUnionNode
	Source         *Source
	Current, next  []int
	Seen           map[uint64][]int
	Arm            SetArm
	scratch        []sqltype.Value
	mark, fallback sqltype.Value
	marksReady     bool
	Phase          uint8
	Opened, closed bool
}

func recursiveCTEOwner(state *State, d *plan.CTEDefinition) *CTEInvocation {
	for owner := state.Ctes; owner != nil; owner = owner.Parent {
		if owner.Frame == d.Owner {
			return owner
		}
	}
	return nil
}

func openRecursiveUnion(ctx context.Context, state *State, n *plan.RecursiveUnionNode) (Stage, error) {
	if err := ctx.Err(); err != nil {
		return Stage{}, err
	}
	owner := recursiveCTEOwner(state, n.Definition)
	if owner == nil || owner.Closed || owner.Producers[n.Definition] == nil {
		return Stage{}, fmt.Errorf("stuntgres: missing recursive CTE producer")
	}
	producer := owner.Producers[n.Definition]
	stage := &recursiveUnionStage{state: *state, Node: n, Source: producer.Source}
	if !n.All && len(n.Schema()) > 0 {
		stage.Seen = make(map[uint64][]int)
	}
	producer.Recursion = stage
	return Stage{Source: stage.Source, Columns: n.Schema(), Selection: []int{}, Produce: stage.produce, Finish: stage.close}, nil
}

func (s *recursiveUnionStage) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.Current, s.next, s.Seen = nil, nil, nil
	s.scratch = nil
	if s.Opened {
		s.Opened = false
		return s.Arm.Close()
	}
	return nil
}

func (s *recursiveUnionStage) produce(ctx context.Context, out *Stage) (bool, error) {
	defer func() { s.Arm.Inner.Evaluation.Context = nil }()
	for s.Phase < 2 {
		if err := util.ExecutionCheckpoint(ctx, "query.recursive"); err != nil {
			return false, err
		}
		// One step advances a row or completes/opens a recursive round.
		if err := workadmission.Admit(ctx, workadmission.WorkRecursiveUnion); err != nil {
			return false, err
		}
		if !s.Opened {
			project, expressions := s.Node.Left, s.Node.LeftExpressions
			if s.Phase == 1 {
				project, expressions = s.Node.Right, s.Node.RightExpressions
			}
			arm, err := openSetArmScratch(ctx, &s.state, project, expressions, s.Node.Schema(), s.scratch)
			if err != nil {
				return false, err
			}
			s.Arm, s.Opened = arm, true
			s.scratch = arm.row
			if c := s.Node.Cycle; c != nil && !s.marksReady {
				evaluation := &s.Arm.Inner.Evaluation
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
		row, present, err := s.Arm.Next(ctx)
		if err != nil {
			return false, err
		}
		if !present {
			s.Opened = false
			if err := s.Arm.Close(); err != nil {
				return false, err
			}
			if s.Phase == 0 {
				s.Phase = 1
			} else {
				s.Current, s.next = s.next, s.Current[:0]
			}
			if len(s.Current) == 0 {
				s.Phase = 2
				return false, nil
			}
			continue
		}
		cycle, search := s.Node.Cycle, s.Node.Search
		var parentSearch, parentCycle sqltype.Value
		if s.Phase == 1 {
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
		if s.Seen != nil {
			for i, family := range s.Node.Grouping {
				slot := s.Node.GroupingSlot(i)
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
			index, err := s.FindTuple(ctx, row, s.Seen[hash])
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
		index := s.Source.FlatCount
		s.Source.Flat = append(s.Source.Flat, row...)
		s.Source.FlatCount++
		if s.Seen != nil {
			s.Seen[hash] = append(s.Seen[hash], index)
		}
		expand := true
		if cycle != nil {
			value, err := cycle.Inequality.Evaluate(ctx, row[cycle.MarkSlot], s.mark, s.state.Input)
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
			if s.Phase == 0 {
				s.Current = append(s.Current, index)
			} else {
				s.next = append(s.next, index)
			}
		}
		out.Selection = append(out.Selection, index)
		return true, nil
	}
	return false, ctx.Err()
}

func openWorktableScan(ctx context.Context, state *State, n *plan.WorktableScanNode) (Stage, error) {
	if err := ctx.Err(); err != nil {
		return Stage{}, err
	}
	owner := recursiveCTEOwner(state, n.CTE)
	if owner == nil || owner.Closed {
		return Stage{}, fmt.Errorf("stuntgres: missing recursive worktable owner")
	}
	producer := owner.Producers[n.CTE]
	if producer == nil || producer.Recursion == nil {
		return Stage{}, fmt.Errorf("stuntgres: missing current recursive generation")
	}
	stage := producer.Recursion
	source := stage.Source
	if n.PresenceSlot != 0 {
		borrowed := *source
		source = &borrowed
		EstablishSourcePresence(source, n.PresenceSlot)
	}
	return Stage{Source: source, Columns: n.Schema(), Selection: stage.Current}, nil
}

func (s *recursiveUnionStage) FindTuple(ctx context.Context, row []sqltype.Value, candidates []int) (int, error) {
	c, search := s.Node.Cycle, s.Node.Search
	if c == nil && search == nil {
		return FindDistinctTuple(ctx, s.Node.Grouping, row, s.Source.Flat, candidates)
	}
	width := len(s.Node.Schema())
	for _, index := range candidates {
		other := s.Source.Flat[index*width : (index+1)*width]
		equal := true
		for i, g := range s.Node.Grouping {
			slot := s.Node.GroupingSlot(i)
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

func SetMultiplicity(operation string, all bool, left, right int) int {
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

type SetArm struct {
	Inner       Stage
	expressions []expr.Expr
	schema      []catalog.Column
	row         []sqltype.Value
}

func openSetArm(ctx context.Context, state *State, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column) (SetArm, error) {
	return openSetArmScratch(ctx, state, project, expressions, schema, nil)
}

func openSetArmScratch(ctx context.Context, state *State, project *plan.ProjectNode, expressions []expr.Expr, schema []catalog.Column, scratch []sqltype.Value) (SetArm, error) {
	innerState := &State{Source: state.Source, SourceMode: state.subquerySourceMode(), Parameters: state.Parameters, Input: state.Input, Output: state.Output, Locking: state.Locking, Outer: state.Outer, Ctes: state.Ctes, hashOwner: state.membershipOwner()}
	innerState.inheritAsyncAdmission(state)
	innerState.foreignOwner = state.foreignOwner
	inner, err := OpenQueryProjectStage(ctx, innerState, project)
	if err != nil {
		return SetArm{}, err
	}
	if scratch == nil {
		scratch = make([]sqltype.Value, len(schema))
	}
	return SetArm{Inner: inner, expressions: expressions, schema: schema, row: scratch}, nil
}

func (a *SetArm) Close() error { return a.Inner.Close() }

// The arm scratch is reused on every pull; only unique keys or demanded UNION
// ALL outputs are retained by the parent. Arm-local clauses execute first.
func (a *SetArm) Next(ctx context.Context) ([]sqltype.Value, bool, error) {
	defer func() { a.Inner.Evaluation.Context = nil }()
	i, present, err := a.Inner.NextOccurrence(ctx)
	if err != nil || !present {
		return nil, present, err
	}
	a.Inner.Evaluation.setContext(ctx)
	a.Inner.Evaluation.BindQueryRow(a.Inner.Source, a.Inner.Index(i))
	a.Inner.Evaluation.OrderValues = a.Inner.OrderRow(i)
	for j, expression := range a.expressions {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		value, err := expression.Eval(&a.Inner.Evaluation)
		if err != nil {
			return nil, false, err
		}
		if err := ValidateProducedValue(value, a.schema[j].Usage); err != nil {
			return nil, false, err
		}
		a.row[j] = value
	}
	return a.row, true, ctx.Err()
}

func OpenSetNode(ctx context.Context, state *State, n *plan.SetNode) (out Stage, err error) {
	if n.Operation == "union" && n.All {
		return openUnionAll(ctx, state, n)
	}
	if len(n.Grouping) != len(n.Schema()) {
		return out, fmt.Errorf("stuntgres: invalid set grouping schema")
	}
	source := &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}
	buckets := make(map[uint64][]int)
	var counts [][2]int
	for side, arm := range [2]*plan.ProjectNode{n.Left, n.Right} {
		expressions := n.LeftExpressions
		if side == 1 {
			expressions = n.RightExpressions
		}
		child, failure := openSetArm(ctx, state, arm, expressions, n.Schema())
		if failure != nil {
			return Stage{}, failure
		}
		failure = func() (failure error) {
			defer func() {
				if closeErr := child.Close(); failure == nil {
					failure = closeErr
				}
			}()
			for {
				row, present, err := child.Next(ctx)
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
				index, err := FindDistinctTuple(ctx, n.Grouping, row, source.Flat, buckets[hash])
				if err != nil {
					return err
				}
				if index < 0 {
					index = source.FlatCount
					buckets[hash] = append(buckets[hash], index)
					source.Flat = append(source.Flat, row...)
					source.FlatCount++
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
			return Stage{}, failure
		}
	}
	out = Stage{Source: source, Columns: n.Schema(), Selection: []int{}}
	for i, count := range counts {
		for copies := SetMultiplicity(n.Operation, n.All, count[0], count[1]); copies > 0; copies-- {
			if err := ctx.Err(); err != nil {
				return Stage{}, err
			}
			out.Selection = append(out.Selection, i)
		}
	}
	return out, ctx.Err()
}

func openUnionAll(ctx context.Context, state *State, n *plan.SetNode) (Stage, error) {
	if !state.asyncAppendEligible() {
		return openSerialUnionAll(ctx, state, n)
	}
	if state.asyncFrontier != nil {
		return openForeignAppend(ctx, state, n, state.asyncFrontier, false)
	}
	return openForeignAppendRoot(ctx, state, n)
}

func openSerialUnionAll(ctx context.Context, state *State, n *plan.SetNode) (Stage, error) {
	out := Stage{Source: &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: state.Source.Authority, Host: state.Source.Host}, Columns: n.Schema(), Selection: []int{}}
	var child SetArm
	opened, side := false, 0
	out.Finish = func() error {
		if opened {
			opened = false
			return child.Close()
		}
		return nil
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
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
			row, present, err := child.Next(ctx)
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
			out.Source.Flat = append(out.Source.Flat, row...)
			out.Selection = append(out.Selection, out.Source.FlatCount)
			out.Source.FlatCount++
			return true, ctx.Err()
		}
		return false, ctx.Err()
	}
	return out, nil
}

// RecursiveUnionStage names the recursive union stage outside exec. The
// defined type keeps its unexported name: the reference cancellation fixture
// waits for a native stack frame containing "recursiveUnion".
type RecursiveUnionStage = recursiveUnionStage
