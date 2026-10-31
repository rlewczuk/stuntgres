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
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowOffsetTarget(t *testing.T) {
	for _, tc := range []struct {
		size, current int
		offset        int32
		reverse       bool
		want          int
		present       bool
	}{
		{4, 0, 1, false, 1, true}, {4, 0, 1, true, 0, false}, {4, 3, 1, false, 0, false}, {4, 3, 1, true, 2, true},
		{4, 2, 0, false, 2, true}, {4, 2, 0, true, 2, true}, {4, 2, -1, false, 1, true}, {4, 2, -1, true, 3, true},
		{4, 0, -1, false, 0, false}, {4, 3, -1, true, 0, false},
		{4, 2, math.MinInt32, false, 0, false}, {4, 2, math.MinInt32, true, 0, false},
		{4, 2, math.MaxInt32, false, 0, false}, {4, 2, math.MaxInt32, true, 0, false},
		{math.MaxInt, math.MaxInt - 1, 1, false, 0, false}, {math.MaxInt, math.MaxInt - 1, 1, true, math.MaxInt - 2, true},
		{math.MaxInt, math.MaxInt - 1, math.MinInt32, true, 0, false},
		{math.MaxInt, math.MaxInt - 1, math.MinInt32, false, math.MaxInt - 1 - 2147483648, true},
	} {
		got, present := exec.WindowOffsetTarget(tc.size, tc.current, tc.offset, tc.reverse)
		if present != tc.present || present && got != tc.want {
			t.Fatalf("%+v => %d/%v", tc, got, present)
		}
	}
	for _, op := range []plan.WindowOperation{plan.WindowLead, plan.WindowLag} {
		if op.FrameDependent() || !op.EvaluatesArgument() {
			t.Fatal("offset demand classification", op)
		}
	}
}

func TestQueryWindowOffsetCancellation(t *testing.T) {
	for _, phase := range []string{"offset", "value", "default"} {
		t.Run(phase, func(t *testing.T) {
			offset := "0"
			if phase == "default" {
				offset = "100"
			}
			query, source := enginetest.QueryFixture(t, engine.New(), "SELECT lead(v,"+offset+",id) OVER(ORDER BY id) FROM t")
			call := &query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := call.Ordinal
			if phase == "value" {
				original = call.Argument
			} else if phase == "default" {
				original = call.DefaultValue
			}
			entered := false
			probe := enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				value, err := original.Eval(c)
				entered = true
				cancel()
				return value, err
			})
			switch phase {
			case "offset":
				call.Ordinal = probe
			case "value":
				call.Argument = probe
			case "default":
				call.DefaultValue = probe
			}
			rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
			if rows != nil {
				rows.Close()
			}
			var d *sqlstate.Error
			if !entered || rows != nil || count != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &d) || source.View != nil {
				t.Fatal("canceled offset window published/leaked", entered, count, err, source.View)
			}
		})
	}
}

func TestQueryWindowOffsetEvaluationRoles(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT lead(v,CASE WHEN id<4 THEN 4-id ELSE 100 END,id) OVER(ORDER BY id) FROM t")
	call := &query.Root.(*plan.ProjectNode).Child.(*plan.WindowNode).Calls[0]
	argument, offset, fallback := call.Argument, call.Ordinal, call.DefaultValue
	var current, selected, defaults []int64
	call.Ordinal = enginetest.QueryProbe(offset.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		current = append(current, id)
		return offset.Eval(c)
	})
	call.Argument = enginetest.QueryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		selected = append(selected, id)
		return argument.Eval(c)
	})
	call.DefaultValue = enginetest.QueryProbe(fallback.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		id, _ := c.Row[0].Integer()
		defaults = append(defaults, id)
		return fallback.Eval(c)
	})
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if count != 4 || !reflect.DeepEqual(current, []int64{1, 2, 3, 4}) || !reflect.DeepEqual(selected, []int64{4, 4, 4}) || !reflect.DeepEqual(defaults, []int64{4}) {
		t.Fatal("lazy role ownership/repeated selection", count, current, selected, defaults)
	}
}
