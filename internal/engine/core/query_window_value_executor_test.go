package core_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowValueMemberSelection(t *testing.T) {
	for _, mode := range []parser.FrameExclusion{parser.ExcludeNoOthers, parser.ExcludeCurrentRow, parser.ExcludeGroup, parser.ExcludeTies} {
		for head := range 6 {
			for tail := range 6 {
				for current := range 5 {
					peerStart, peerEnd := current, current+1
					if current < 2 {
						peerStart, peerEnd = 0, 2
					}
					var admitted []int
					for member := head; member < tail; member++ {
						include := true
						switch mode {
						case parser.ExcludeCurrentRow:
							include = member != current
						case parser.ExcludeGroup:
							include = member < peerStart || member >= peerEnd
						case parser.ExcludeTies:
							include = member == current || member < peerStart || member >= peerEnd
						}
						if include {
							admitted = append(admitted, member)
						}
					}
					for _, reverse := range []bool{false, true} {
						for _, ordinal := range []int64{-1, 0, 1, 2, 3, 4, 5, math.MaxInt64} {
							member, present := exec.WindowValueMember(mode, head, tail, current, peerStart, peerEnd, ordinal, reverse)
							wantPresent := ordinal >= 0 && ordinal < int64(len(admitted))
							if present != wantPresent {
								t.Fatal(mode, head, tail, current, ordinal, reverse, present, admitted)
							}
							if present {
								index := int(ordinal)
								if reverse {
									index = len(admitted) - 1 - index
								}
								if member != admitted[index] {
									t.Fatal(mode, head, tail, current, ordinal, reverse, member, admitted[index])
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestQueryWindowValueEvaluationCancellation(t *testing.T) {
	for _, phase := range []string{"ordinal", "value"} {
		t.Run(phase, func(t *testing.T) {
			query, source := enginetest.QueryFixture(t, engine.New(), "SELECT nth_value(v,id) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t")
			node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := &node.Calls[0]
			original := call.Ordinal
			if phase == "value" {
				original = call.Argument
			}
			entered := false
			probe := enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered = true
				cancel()
				return value, err
			})
			if phase == "value" {
				call.Argument = probe
			} else {
				call.Ordinal = probe
			}
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var d *sqlstate.Error
			if !entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &d) || source.View != nil {
				t.Fatal("canceled value window published/leaked", entered, count, err, source.View)
			}
		})
	}
}

func TestQueryWindowValueCurrentOrdinalAndSelectedEvaluation(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT nth_value(v,CASE WHEN id%2=1 THEN 1 ELSE 2 END) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode)
	call := &node.Calls[0]
	argument, ordinal := call.Argument, call.Ordinal
	var current, selected []int64
	call.Ordinal = enginetest.QueryProbe(ordinal.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		current = append(current, id)
		return ordinal.Eval(c)
	})
	call.Argument = enginetest.QueryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		selected = append(selected, id)
		return argument.Eval(c)
	})
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if count != 4 || !reflect.DeepEqual(current, []int64{1, 2, 3, 4}) || !reflect.DeepEqual(selected, []int64{1, 2, 1, 2}) {
		t.Fatal("current/selected context or repeated-value cache", count, current, selected)
	}
	values := make([]sqltype.Value, 1)
	for i := range 4 {
		present, err := rows.Next(context.Background(), values)
		if !present || err != nil || values[0].IsNull() != (i%2 == 1) {
			t.Fatal(i, present, values, err)
		}
		if i%2 == 0 {
			number, err := values[0].Integer()
			if err != nil || number != 1 {
				t.Fatal(number, err)
			}
		}
	}
}

func TestQueryWindowValueOutputPublicationCancellation(t *testing.T) {
	for _, phase := range []string{"output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			query, source := enginetest.QueryFixture(t, engine.New(), "SELECT nth_value(v,id) OVER w,count(*) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE CURRENT ROW)")
			continuation, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := exec.Finalize(probe, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var diagnostic *sqlstate.Error
			if !probe.entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) || source.View != nil {
				t.Fatal("value output/Finish cancellation", probe.entered, count, err, source.View)
			}
			recovered, count, err := exec.Finalize(context.Background(), query.Root, continuation, nil)
			if err != nil || recovered == nil || count != 4 {
				t.Fatal("same bound graph recovery", count, err)
			}
			recovered.Close()
		})
	}
}
