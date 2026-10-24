package exec

import (
	"context"
	"fmt"
	"reflect"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
)

// Compatible nested appends share request admission, not expression evaluation.
// Their ordinary SetArm hierarchy preserves every bound projection/coercion.
// Only the root waits; nested append stages propagate the private pending token.
type foreignAppendFrontier struct {
	admitted   map[*plan.SourceNode]*ForeignExecution
	executions []*ForeignExecution
	waitCases  []reflect.SelectCase
}

func (f *foreignAppendFrontier) probe(ctx context.Context, s *State, node plan.Node) error {
	switch n := node.(type) {
	case *plan.ProjectNode:
		if n.CTEOwner || n.HasTargetSet || n.RuleInvocation {
			return nil
		}
		return f.probe(ctx, s, n.Child)
	case *plan.FilterNode:
		return f.probe(ctx, s, n.Child)
	case *plan.SetNode:
		if n.Operation != "union" || !n.All {
			return nil
		}
		if err := f.probe(ctx, s, n.Left); err != nil {
			return err
		}
		return f.probe(ctx, s, n.Right)
	case *plan.SourceNode:
		source := s.Source
		if source != nil && source.Sources != nil {
			if n.Occurrence < 0 || n.Occurrence >= len(source.Sources) {
				return fmt.Errorf("stuntgres: invalid async query occurrence")
			}
			source = source.Sources[n.Occurrence]
		}
		if source == nil || source.Foreign == nil {
			return nil
		}
		foreign := source.Foreign
		if _, ok := foreign.Wrapper.(catalog.ForeignAsyncScanProvider); !ok {
			return nil
		}
		if host := source.LiveHost(); host != nil {
			host.ReleaseReplacementRead()
		}
		parameters, err := ForeignParameters(ctx, s, foreign)
		if err != nil {
			return err
		}
		request := CopyForeignRequest(foreign.Request)
		request.Parameters = parameters
		execution := &ForeignExecution{owner: s.foreignOwner, asyncProbe: true}
		if err := foreign.Plan(ctx, request, execution); err != nil {
			return err
		}
		if execution.async.capable {
			if f.admitted == nil {
				f.admitted = make(map[*plan.SourceNode]*ForeignExecution)
			}
			f.admitted[n] = execution
		}
	}
	return nil
}
func (f *foreignAppendFrontier) contains(node plan.Node) bool {
	switch n := node.(type) {
	case *plan.SourceNode:
		return f.admitted[n] != nil
	case *plan.ProjectNode:
		return !n.CTEOwner && !n.HasTargetSet && !n.RuleInvocation && f.contains(n.Child)
	case *plan.FilterNode:
		return f.contains(n.Child)
	case *plan.SetNode:
		return n.Operation == "union" && n.All && (f.contains(n.Left) || f.contains(n.Right))
	}
	return false
}
func (f *foreignAppendFrontier) replenish(ctx context.Context) error {
	for _, execution := range f.executions {
		if err := execution.requestAsync(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (f *foreignAppendFrontier) wait(ctx context.Context) error {
	if cap(f.waitCases) < len(f.executions)+1 {
		f.waitCases = make([]reflect.SelectCase, 0, len(f.executions)+1)
	}
	cases := f.waitCases[:1]
	cases[0] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
	for _, execution := range f.executions {
		if execution.async.request != nil && execution.async.ready != nil {
			cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(execution.async.ready)})
		}
	}
	if len(cases) == 1 {
		return fmt.Errorf("stuntgres: pending append without a foreign request")
	}
	selected, _, _ := reflect.Select(cases)
	clear(cases)
	f.waitCases = cases[:0]
	if selected == 0 {
		return ctx.Err()
	}
	return nil
}
func foreignAppendOutput(s *State, n *plan.SetNode) Stage {
	return Stage{Source: &Source{FlatWidth: len(n.Schema()), Materialized: true, Authority: s.Source.Authority, Host: s.Source.Host}, Columns: n.Schema(), Selection: []int{}}
}

// Discovery and priming start on first demand, never during DECLARE or opening
// an unused ordinary UNION ALL arm. With no opt-in retain the serial stage.
func openForeignAppendRoot(ctx context.Context, s *State, n *plan.SetNode) (Stage, error) {
	out := foreignAppendOutput(s, n)
	var inner Stage
	initialized := false
	out.Finish = func() error {
		if initialized {
			return inner.Close()
		}
		return nil
	}
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		if !initialized {
			frontier := &foreignAppendFrontier{}
			if err := frontier.probe(ctx, s, n); err != nil {
				return false, err
			}
			var err error
			if len(frontier.admitted) == 0 {
				inner, err = openSerialUnionAll(ctx, s, n)
			} else {
				local := *s
				local.asyncFrontier = frontier
				inner, err = openForeignAppend(ctx, &local, n, frontier, true)
			}
			initialized = true
			if err != nil {
				return false, err
			}
		}
		return inner.Produce(ctx, out)
	}
	return out, nil
}

func openForeignAppend(ctx context.Context, s *State, n *plan.SetNode, f *foreignAppendFrontier, root bool) (Stage, error) {
	out := foreignAppendOutput(s, n)
	var arms [2]SetArm
	var opened, done [2]bool
	projects := [2]*plan.ProjectNode{n.Left, n.Right}
	expressions := [2][]expr.Expr{n.LeftExpressions, n.RightExpressions}
	closeArms := func() error {
		var failure error
		for i := range arms {
			if opened[i] {
				opened[i] = false
				if err := arms[i].Close(); failure == nil {
					failure = err
				}
			}
		}
		return failure
	}
	open := func(ctx context.Context, i int) error {
		local := *s
		if !f.contains(projects[i]) {
			local.asyncFrontier = nil
		}
		arm, err := openSetArm(ctx, &local, projects[i], expressions[i], n.Schema())
		if err != nil {
			return err
		}
		arms[i], opened[i] = arm, true
		return nil
	}
	// Opening only admitted paths recursively registers every compatible foreign
	// leaf without demanding any local projection or synchronous sibling.
	for i := range arms {
		if f.contains(projects[i]) {
			if err := open(ctx, i); err != nil {
				_ = closeArms()
				return Stage{}, err
			}
		}
	}
	if root {
		for _, execution := range f.executions {
			if err := execution.async.start(ctx); err != nil {
				_ = closeArms()
				return Stage{}, err
			}
		}
		if err := f.replenish(ctx); err != nil {
			_ = closeArms()
			return Stage{}, err
		}
	}
	out.Finish = closeArms
	next := 0
	out.Produce = func(ctx context.Context, out *Stage) (bool, error) {
		if root {
			if err := f.replenish(ctx); err != nil {
				return false, err
			}
		}
		for {
			for count := range 2 {
				side := (next + count) % 2
				if done[side] {
					continue
				}
				if !opened[side] {
					if err := open(ctx, side); err != nil {
						return false, err
					}
				}
				row, present, err := arms[side].Next(ctx)
				if err == errForeignPending {
					continue
				}
				if err != nil {
					return false, err
				}
				if !present {
					done[side] = true
					opened[side] = false
					if err := arms[side].Close(); err != nil {
						return false, err
					}
					continue
				}
				next = (side + 1) % 2
				out.Source.Flat = append(out.Source.Flat, row...)
				out.Selection = append(out.Selection, out.Source.FlatCount)
				out.Source.FlatCount++
				return true, ctx.Err()
			}
			if done[0] && done[1] {
				return false, ctx.Err()
			}
			if !root {
				return false, errForeignPending
			}
			if err := f.wait(ctx); err != nil {
				return false, err
			}
		}
	}
	return out, nil
}
