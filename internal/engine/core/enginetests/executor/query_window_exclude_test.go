package executor_test

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowExcludeMask(t *testing.T) {
	// Explicit admitted-member lists, independently specified for all four modes.
	for _, tc := range []struct {
		head, tail, current, first, last int
		want                             [4][]int
	}{
		{0, 3, 0, 0, 2, [4][]int{{0, 1, 2}, {1, 2}, {2}, {0, 2}}},
		{0, 3, 1, 0, 2, [4][]int{{0, 1, 2}, {0, 2}, {2}, {1, 2}}},
		{0, 1, 0, 0, 2, [4][]int{{0}, {}, {}, {0}}},
		{1, 3, 0, 0, 2, [4][]int{{1, 2}, {1, 2}, {2}, {2}}},
		{0, 1, 2, 1, 3, [4][]int{{0}, {0}, {0}, {0}}},
		{0, 3, 1, 0, 3, [4][]int{{0, 1, 2}, {0, 2}, {}, {1}}},
		{2, 2, 1, 0, 3, [4][]int{{}, {}, {}, {}}},
		{3, 1, 1, 0, 3, [4][]int{{}, {}, {}, {}}},
	} {
		for mode, want := range tc.want {
			got := []int{}
			for member := tc.head; member < tc.tail; member++ {
				if exec.WindowMaskAdmits(parser.FrameExclusion(mode), member, tc.current, tc.first, tc.last) {
					got = append(got, member)
				}
			}
			if !reflect.DeepEqual(got, want) || exec.WindowMaskCount(parser.FrameExclusion(mode), tc.head, tc.tail, tc.current, tc.first, tc.last) != len(want) {
				t.Fatal(tc, mode, got, want)
			}
		}
	}
}

func TestQueryWindowExcludeEndpoints(t *testing.T) {
	ctx := context.Background()
	peers := []int{2, 3}
	for row := range 3 {
		first, last, group := 0, 2, 0
		if row == 2 {
			first, last, group = 2, 3, 1
		}
		for _, unit := range []parser.FrameUnit{parser.FrameRows, parser.FrameGroups, parser.FrameRange} {
			var head, tail int
			var err error
			switch unit {
			case parser.FrameRows:
				head, err = exec.WindowRowsEndpoint(ctx, parser.FrameCurrentRow, 0, row, 3, false)
				if err == nil {
					tail, err = exec.WindowRowsEndpoint(ctx, parser.FrameCurrentRow, 0, row, 3, true)
				}
			case parser.FrameGroups:
				head, err = exec.WindowGroupsEndpoint(ctx, parser.FrameCurrentRow, 0, peers, group, false)
				if err == nil {
					tail, err = exec.WindowGroupsEndpoint(ctx, parser.FrameCurrentRow, 0, peers, group, true)
				}
			case parser.FrameRange:
				spec := plan.WindowSpec{}
				bound := plan.WindowBound{Kind: parser.FrameCurrentRow}
				head, err = exec.WindowFrameEndpoint(ctx, spec, bound, sqltype.Value{}, exec.WindowOrderState{}, []int{0, 1, 2}, first, last, 0, false, sqltype.InputEnvironment{})
				if err == nil {
					tail, err = exec.WindowFrameEndpoint(ctx, spec, bound, sqltype.Value{}, exec.WindowOrderState{}, []int{0, 1, 2}, first, last, 0, true, sqltype.InputEnvironment{})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			want := [4]int{last - first, last - first - 1, 0, 1}
			if unit == parser.FrameRows {
				want = [4]int{1, 0, 0, 1}
			}
			for mode, n := range want {
				if got := exec.WindowMaskCount(parser.FrameExclusion(mode), head, tail, row, first, last); got != n {
					t.Fatal(unit, row, mode, got, n)
				}
			}
		}
	}
}

type excludeCancelContext struct {
	context.Context
	cancel   context.CancelFunc
	phase    string
	entered  bool
	computed bool
}

func (c *excludeCancelContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		window, hit, sum := false, false, false
		finalize, publication := false, false
		caller := ""
		for {
			frame, more := frames.Next()
			if caller == "" {
				caller = frame.Function
			}
			window = window || strings.HasSuffix(frame.Function, ".openWindowNode")
			sum = sum || strings.HasSuffix(frame.Function, ".(*WindowAggregates).Evaluate")
			hit = hit || strings.Contains(frame.Function, c.phase)
			finalize = finalize || strings.HasSuffix(frame.Function, ".IntegralSum.Finalize")
			publication = publication || strings.HasSuffix(frame.Function, "exec.Finalize")
			if !more {
				break
			}
		}
		c.computed = c.computed || window && finalize
		if c.phase == "sum-output" {
			hit = c.computed && strings.HasSuffix(caller, ".openWindowNode")
		}
		if c.phase == "sum-publication" {
			hit = c.computed && publication && !window
		}
		if c.phase == "ColumnExpr.Eval" {
			hit = hit && sum
		}
		if hit && (window || c.phase == "sum-publication") {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}

func TestQueryWindowExcludeCancellation(t *testing.T) {
	for _, exclusion := range []string{" EXCLUDE TIES", ""} {
		for _, phase := range []string{"CompareWindowRows", "WindowFrameCount", "IntegralSum.Init", "IntegralSum.Advance", "IntegralSum.Finalize", "ColumnExpr.Eval", "sum-output", "sum-publication"} {
			t.Run(phase+exclusion, func(t *testing.T) {
				query, source := enginetest.QueryFixture(t, engine.New(), "SELECT count(*) OVER w,sum(v) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING"+exclusion+")")
				continuation, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
				if err != nil {
					t.Fatal(err)
				}
				defer continuation.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				probe := &excludeCancelContext{Context: ctx, cancel: cancel, phase: phase}
				rows, n, err := exec.Finalize(probe, query.Root, source, nil)
				if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || n != 0 || source.View != nil {
					t.Fatal(probe.entered, rows, n, err)
				}
				source.Close()
				source.Close()
				recovered, n, err := exec.Finalize(context.Background(), query.Root, continuation, nil)
				if err != nil || recovered == nil || n != 4 {
					t.Fatal(recovered, n, err)
				}
				recovered.Close()
				recovered.Close()
			})
		}
	}
}

func TestQueryWindowExcludeFinishAtomicity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE s(id INT)")
	enginetest.Run(t, e, "INSERT INTO s VALUES(1),(2),(3)")
	enginetest.Run(t, e, "CREATE TABLE d(n BIGINT)")
	enginetest.Run(t, e, "INSERT INTO d VALUES(99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO d SELECT sum(id) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW) FROM s"), 0, nil)
	if err != nil || result.Count != 3 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !scope.Finished || scope.View != nil || scope.LockTicket != nil {
		t.Fatal("statement resources survived Finish")
	}
	current := enginetest.Run(t, e, "SELECT n FROM d").Rows
	defer current.Close()
	tuple := make([]sqltype.Value, 1)
	present, err := current.Next(context.Background(), tuple)
	value, _ := tuple[0].Integer()
	if !present || err != nil || value != 99 {
		t.Fatal(present, err, value)
	}
	if present, err := current.Next(context.Background(), tuple); present || err != nil {
		t.Fatal(present, err)
	}
	enginetest.Run(t, e, "INSERT INTO d SELECT sum(id) OVER(ROWS CURRENT ROW EXCLUDE TIES) FROM s")
}

func TestQueryWindowExcludeGroupedArgument(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT v,sum(count(*)) OVER(ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES) FROM t GROUP BY v")
	defer source.Close()
	window := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	if _, ok := window.Calls[0].Argument.(plan.GroupExpr); !ok {
		t.Fatal("grouped SUM argument lost aggregate slot", window.Calls[0].Argument)
	}
	rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || n != 3 {
		t.Fatal(rows, n, err)
	}
	defer rows.Close()
	tuple := make([]sqltype.Value, 2)
	for range 3 {
		present, err := rows.Next(context.Background(), tuple)
		// No window ORDER: TIES keeps each grouped occurrence's own COUNT.
		if !present || err != nil || tuple[1].IsNull() || tuple[1].Type().ID() != sqltype.Numeric {
			t.Fatal(present, tuple, err)
		}
	}
}
