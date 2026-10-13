package engine

import (
	"context"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Instrument argument occurrences, not providers or the executor. Evaluating the
// real bound child still checks row binding; the replacement value exposes the
// actual transition schedule through the existing typed SUM providers.
type windowSumArgumentProbe struct {
	BoundExpr
	sequence   *int64
	visits     *[]int
	occurrence int
}

func (p windowSumArgumentProbe) Eval(c *EvalContext) (sqltype.Value, error) {
	if _, err := p.BoundExpr.Eval(c); err != nil {
		return sqltype.Value{}, err
	}
	*p.sequence++
	*p.visits = append(*p.visits, p.occurrence)
	return sqltype.Integer(p.ResultUsage().Type, *p.sequence)
}

func TestQueryWindowExcludeTransitionLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      parser.FrameExclusion
		endpoints [][2]int
		want      []string
		draws     int64
	}{
		{"full", parser.ExcludeNoOthers, [][2]int{{0, 3}, {0, 3}, {0, 3}}, []string{"6", "6", "6"}, 3},
		{"growing", parser.ExcludeNoOthers, [][2]int{{0, 1}, {0, 2}, {0, 3}}, []string{"1", "3", "6"}, 3},
		{"moving-head", parser.ExcludeNoOthers, [][2]int{{0, 2}, {1, 3}, {2, 3}}, []string{"3", "7", "5"}, 5},
		{"nonoverlap", parser.ExcludeNoOthers, [][2]int{{0, 1}, {2, 3}}, []string{"1", "2"}, 2},
		{"reversed-empty", parser.ExcludeNoOthers, [][2]int{{2, 1}, {2, 2}}, []string{"NULL", "NULL"}, 0},
		{"ties-admits-all", parser.ExcludeTies, [][2]int{{0, 3}, {0, 3}, {0, 3}}, []string{"6", "15", "24"}, 9},
		{"group-masks-all", parser.ExcludeGroup, [][2]int{{0, 3}, {0, 3}, {0, 3}}, []string{"NULL", "NULL", "NULL"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT sum(v) OVER(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t")
			defer source.close()
			project := query.Root.(*projectNode)
			node := project.child.(*windowNode)
			state := &queryState{source: source, frame: project.frame}
			child, err := openQueryNode(context.Background(), state, node.child)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Close()
			if err := child.drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			calls := append([]windowCall(nil), node.calls...)
			var sequence int64
			visits := []int{}
			calls[0].argument = windowSumArgumentProbe{calls[0].argument, &sequence, &visits, 0}
			frame := node.specs[0].frame
			frame.exclusion = tc.mode
			sums := windowSums{states: []windowSumState{{callIndex: 0}}}
			for output, bounds := range tc.endpoints {
				previous := sums.states[0].aggregate
				peerStart, peerEnd := output, output+1
				if tc.mode == parser.ExcludeGroup {
					peerStart, peerEnd = 0, 3
				}
				if err := sums.evaluate(context.Background(), state, &child, calls, []int{0, 1, 2}, frame, bounds[0], bounds[1], output, peerStart, peerEnd); err != nil {
					t.Fatal(err)
				}
				if previous != nil {
					retained := tc.name == "full" || tc.name == "growing"
					if (sums.states[0].aggregate == previous) != retained {
						t.Fatal("provider transition ownership", tc.name, output)
					}
				}
				got := "NULL"
				if !sums.states[0].result.IsNull() {
					got, err = sums.states[0].result.Type().Output(context.Background(), sums.states[0].result)
					if err != nil {
						t.Fatal(err)
					}
				}
				if got != tc.want[output] {
					t.Fatalf("output %d got %s want %s", output, got, tc.want[output])
				}
			}
			if sequence != tc.draws {
				t.Fatal("argument evaluations", sequence, tc.draws)
			}
			// A new partition drops transition ownership even when bounds are identical.
			previous := sums.states[0].aggregate
			sums.initialized = false
			if err := sums.evaluate(context.Background(), state, &child, calls, []int{0, 1, 2}, frame, 0, 1, 0, 0, 1); err != nil {
				t.Fatal(err)
			}
			if sums.states[0].aggregate == previous {
				t.Fatal("new partition retained provider state")
			}
			if tc.mode != parser.ExcludeGroup && sequence != tc.draws+1 {
				t.Fatal("partition state reused", sequence)
			}
		})
	}
}

func TestQueryWindowExcludeMemberMajorTypedStates(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT sum(v) OVER w,sum(v::bigint) OVER w FROM t WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING EXCLUDE TIES)")
	defer source.close()
	project := query.Root.(*projectNode)
	node := project.child.(*windowNode)
	state := &queryState{source: source, frame: project.frame}
	child, err := openQueryNode(context.Background(), state, node.child)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if err := child.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	calls := append([]windowCall(nil), node.calls...)
	var sequence int64
	visits := []int{}
	for i := range calls {
		calls[i].argument = windowSumArgumentProbe{calls[i].argument, &sequence, &visits, i}
	}
	for execution := range 2 {
		sums := windowSums{states: []windowSumState{{callIndex: 0}, {callIndex: 1}}}
		for output := range 3 {
			if err := sums.evaluate(context.Background(), state, &child, calls, []int{0, 1, 2}, node.specs[0].frame, 0, 3, output, output, output+1); err != nil {
				t.Fatal(err)
			}
			for i, entry := range sums.states {
				want := int64(9 + i*3 + output*18 + execution*54)
				text, err := entry.result.Type().Output(context.Background(), entry.result)
				if err != nil || text != strconv.FormatInt(want, 10) || entry.result.Type() != calls[i].expression.ResultUsage().Type {
					t.Fatal(execution, output, i, text, want, err)
				}
			}
		}
	}
	wantVisits := []int{}
	for range 18 {
		wantVisits = append(wantVisits, 0, 1)
	}
	if sequence != 36 || !reflect.DeepEqual(visits, wantVisits) {
		t.Fatal(sequence, visits)
	}
}

func TestQueryWindowExcludeFinalizeThenAdvance(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		provider, input := sumUnitProvider(e, id)
		state, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := provider.Finalize(ctx, state)
		if err != nil || !empty.IsNull() {
			t.Fatal(empty, err)
		}
		for _, tc := range []struct{ input, want string }{{"2", "2"}, {"-2", "0"}, {"3", "3"}} {
			value, err := input.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := provider.Advance(ctx, state, value, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
			value, err = provider.Finalize(ctx, state)
			if err != nil || value.IsNull() {
				t.Fatal(value, err)
			}
			text, err := value.Type().Output(ctx, value)
			if err != nil || text != tc.want {
				t.Fatal(id, text, tc.want, err)
			}
		}
	}
}

