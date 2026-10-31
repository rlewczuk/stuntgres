package exec

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"runtime/pprof"
	"strings"
	"testing"
	"testing/synctest"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestSortOrderedAggregateExecutionPause(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	for _, tc := range []struct {
		name                   string
		keys                   []string
		text, desc, nullsFirst bool
		want                   []int
	}{
		{name: "ascending", keys: []string{"40", "10", "30", "20"}, want: []int{1, 3, 2, 0}},
		{name: "descending", keys: []string{"40", "10", "30", "20"}, desc: true, want: []int{0, 2, 3, 1}},
		{name: "stable", keys: []string{"20", "10", "20", "10"}, want: []int{1, 3, 0, 2}},
		{name: "nulls_first", keys: []string{"20", "NULL", "10", "NULL"}, nullsFirst: true, want: []int{1, 3, 2, 0}},
		{name: "nulls_last_desc", keys: []string{"20", "NULL", "10", "NULL"}, desc: true, want: []int{0, 2, 1, 3}},
		{name: "text_C", keys: []string{"a", "Z", "aa", "A"}, text: true, want: []int{3, 1, 0, 2}},
	} {
		for _, cancelHeld := range []bool{false, true} {
			name := tc.name + "/resume"
			if cancelHeld {
				name = tc.name + "/cancel"
			}
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					d := types[sqltype.Float8]
					if tc.text {
						d = types[sqltype.Text]
					}
					usage := d.Usage()
					if tc.text {
						usage.Collation = sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}
					}
					comparison, err := sqltype.BindOrdering(usage)
					if err != nil {
						t.Fatal(err)
					}
					call := plan.AggregateCall{StageWidth: 2, Order: []plan.SortKey{{Comparison: comparison, Cache: 0, Desc: tc.desc, NullsFirst: tc.nullsFirst}}}
					var tuples []sqltype.Value
					for i, key := range tc.keys {
						value := sqltype.Null(d)
						if key != "NULL" {
							value, err = d.Input(context.Background(), key, -1)
							if err != nil {
								t.Fatal(err)
							}
						}
						marker, _ := sqltype.Integer(types[sqltype.Int4], int64(i))
						tuples = append(tuples, value, marker)
					}
					saved := append([]sqltype.Value(nil), tuples...)
					keys := append([]plan.SortKey(nil), call.Order...)
					backing, orderBacking := &tuples[0], &call.Order[0]
					assertImmutable := func() {
						t.Helper()
						if &tuples[0] != backing || &call.Order[0] != orderBacking || !reflect.DeepEqual(tuples, saved) || !reflect.DeepEqual(call.Order, keys) {
							t.Fatal("sort mutated borrowed tuples or comparison metadata")
						}
						got, conflict := call.Order[0].Comparison.InputCollation()
						want, wantConflict := comparison.InputCollation()
						if got != want || conflict != wantConflict {
							t.Fatal("collation authority changed")
						}
					}
					pause := util.NewExecutionPause("query.aggregate.sort")
					ctx, cancel := context.WithCancelCause(util.ActivateExecutionPause(util.WithExecutionPause(context.Background(), pause)))
					type outcome struct {
						input OrderedAggregateInput
						err   error
					}
					done := make(chan outcome, 1)
					joined := false
					defer func() {
						cancel(context.Canceled)
						pause.Resume()
						if !joined {
							<-done
						}
					}()
					go func() { in, err := sortOrderedAggregate(ctx, &State{}, call, tuples); done <- outcome{in, err} }()
					synctest.Wait()
					select {
					case out := <-done:
						joined = true
						t.Fatal("sort finished without its hold", out.err)
					default:
					}
					if err := pause.Wait(ctx); err != nil {
						t.Fatal(err)
					}
					// Admission alone is not a live-frame witness: require the
					// checkpoint and actual comparator in one parked worker stack.
					var stack bytes.Buffer
					if err := pprof.Lookup("goroutine").WriteTo(&stack, 2); err != nil {
						t.Fatal(err)
					}
					live := false
					for _, record := range strings.Split(stack.String(), "\n\n") {
						if strings.Contains(record, "stuntgres/internal/engine/exec.sortOrderedAggregate.func1(") && strings.Contains(record, "stuntgres/internal/util.ExecutionCheckpoint(") {
							live = true
							break
						}
					}
					if !live {
						t.Fatal("admitted hold did not retain real comparator frame")
					}
					assertImmutable()
					cause := errors.New("held real-sort cancellation")
					if cancelHeld {
						cancel(cause)
					} else {
						pause.Resume()
					}
					out := <-done
					joined = true
					assertImmutable()
					if cancelHeld {
						if out.err != context.Canceled || context.Cause(ctx) != cause || out.input.Values != nil || out.input.Indices != nil || out.input.Order != nil || out.input.Width != 0 {
							t.Fatal("cancellation published partial sort", out, context.Cause(ctx))
						}
					} else if out.err != nil || !reflect.DeepEqual(out.input.Indices, tc.want) || &out.input.Values[0] != backing || &out.input.Order[0] != orderBacking || out.input.Width != 2 {
						t.Fatal("typed stable sort result", out.input.Indices, tc.want, out.err)
					}
				})
			})
		}
	}

	for _, mode := range []string{"absent", "inert", "wrong_point", "empty", "singleton", "comparison_error", "borrowed_comparison"} {
		t.Run(mode, func(t *testing.T) {
			d := types[sqltype.Float8]
			comparison, err := sqltype.BindOrdering(d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			call := plan.AggregateCall{StageWidth: 1, Order: []plan.SortKey{{Comparison: comparison, Cache: 0}}}
			one, _ := sqltype.Float(d, 1)
			two, _ := sqltype.Float(d, 2)
			tuples := []sqltype.Value{two, one}
			point := "query.aggregate.sort"
			if mode == "wrong_point" {
				point = "query.aggregate"
			}
			pause := util.NewExecutionPause(point)
			defer pause.Resume()
			ctx := context.Background()
			if mode != "absent" {
				ctx = util.WithExecutionPause(ctx, pause)
			}
			if mode != "inert" {
				ctx = util.ActivateExecutionPause(ctx)
			}
			want := []int{1, 0}
			switch mode {
			case "empty":
				tuples = nil
				want = []int{}
			case "singleton":
				tuples = tuples[:1]
				want = []int{0}
			case "comparison_error":
				tuples[0] = sqltype.String(types[sqltype.Text], "wrong typed value")
			}
			if mode == "borrowed_comparison" {
				input := OrderedAggregateInput{Order: call.Order}
				sign, err := input.Borrowed(nil).Compare(ctx, []sqltype.Value{one}, []sqltype.Value{two}, sqltype.InputEnvironment{})
				if sign != -1 || err != nil {
					t.Fatal(sign, err)
				}
			} else {
				out, err := sortOrderedAggregate(ctx, &State{}, call, tuples)
				if mode == "comparison_error" {
					if err == nil || err.Error() != "invalid comparison values" || out.Values != nil || out.Indices != nil {
						t.Fatal("original comparison failure", out, err)
					}
					var diagnostic interface{ SQLState() string }
					if errors.As(err, &diagnostic) {
						t.Fatal("ordinary typed failure gained SQLSTATE", err)
					}
				} else if err != nil || !reflect.DeepEqual(out.Indices, want) {
					t.Fatal(out.Indices, want, err)
				}
			}
			pause.Resume()
			if err := pause.Wait(context.Background()); err == nil || err.Error() != "stuntgres: execution pause resumed before admission" {
				t.Fatal("false sort admission", err)
			}
		})
	}
}
