package enginetests_test

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryOuterJoinsZeroWidthOccurrences(t *testing.T) {
	e := engine.New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		for _, truth := range []bool{false, true} {
			join := &plan.JoinNode{Kind: kind, Left: &plan.SourceNode{Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, truth)}}
			source := &exec.Source{Sources: []*exec.Source{{Anonymous: [][]sqltype.Value{{}, {}}}, {Anonymous: [][]sqltype.Value{{}, {}, {}}}}}
			stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: source}, join)
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.Drain(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 6
			if !truth {
				want = 2
				if kind == parser.JoinRight {
					want = 3
				}
				if kind == parser.JoinFull {
					want = 5
				}
			}
			if stage.Count() != want || stage.Source.FlatCount != want || len(stage.Source.Flat) != 0 {
				t.Fatal(kind, truth, stage.Count(), stage.Source)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestQueryOuterJoinsMissingChildDoesNotEvaluateRecipe(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	value, _ := sqltype.Integer(integer, 7)
	schema := plan.QuerySchema{{Name: "k", Usage: integer.Usage()}}
	calls := 0
	missing := &plan.JoinNode{QuerySchema: schema, Left: &plan.SourceNode{Occurrence: 1}, Right: &plan.SourceNode{Occurrence: 2}, Merges: []expr.Expr{queryProbe(integer.Usage(), func(*exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("missing recipe evaluated")
	})}}
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), schema...), schema...), Kind: parser.JoinLeft, Left: &plan.SourceNode{QuerySchema: schema, Occurrence: 0}, Right: missing, LeftWidth: 1, RightWidth: 1, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}}
	source := &exec.Source{Sources: []*exec.Source{{Anonymous: [][]sqltype.Value{{value}}}, {Anonymous: [][]sqltype.Value{}}, {Anonymous: [][]sqltype.Value{}}}}
	stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: source}, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	row := stage.Source.Row(stage.Index(0))
	if stage.Count() != 1 || calls != 0 || len(row) != 2 || !row[1].IsNull() || row[1].Type() != integer {
		t.Fatal(calls, row)
	}
}

func TestQueryOuterJoinsRejectedProductAllocation(t *testing.T) {
	e := engine.New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	// Retained frames/flags may grow with demanded child and emitted occurrences,
	// never with the rejected pair matrix. Zero-width isolates that storage.
	allocated := func(size int) uint64 {
		inputs := make([][]sqltype.Value, size)
		for i := range inputs {
			inputs[i] = []sqltype.Value{}
		}
		source := &exec.Source{Sources: []*exec.Source{{Anonymous: inputs}, {Anonymous: inputs}}}
		join := &plan.JoinNode{Kind: parser.JoinFull, Left: &plan.SourceNode{Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: source}, join)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stage.Count() != 2*size {
			t.Fatal(stage.Count())
		}
		stage.Close()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	small, big := allocated(32), allocated(256)
	if big > small*12+32768 {
		t.Fatalf("rejected product storage grew quadratically: %d -> %d", small, big)
	}
}

func TestQueryOuterJoinsCloseOnceAndPrimaryError(t *testing.T) {
	e := engine.New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		closed := [2]int{}
		closeFailure := errors.New("child close")
		primary := errors.New("predicate primary")
		bridge := func(which int) *exec.Source {
			inner := &exec.Stage{Source: &exec.Source{Anonymous: [][]sqltype.Value{{}}}, Evaluation: exec.Context{}, Finish: func() error { closed[which]++; return closeFailure }}
			return &exec.Source{Materialized: true, NestedRoot: &plan.ProjectNode{Child: &plan.SingletonNode{}}, NestedStage: inner}
		}
		inputs := &exec.Source{Sources: []*exec.Source{bridge(0), bridge(1)}}
		join := &plan.JoinNode{Kind: kind, Left: &plan.SourceNode{Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}, Predicate: queryProbe(boolean.Usage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, primary })}
		rows, _, err := exec.Finalize(context.Background(), &plan.ProjectNode{Child: join}, inputs, nil)
		if rows != nil || !errors.Is(err, primary) || closed != [2]int{1, 1} {
			t.Fatal(kind, rows, err, closed)
		}
	}
}

func TestQueryOuterJoinsNullRowsAreNotAbsentOccurrences(t *testing.T) {
	e := engine.New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	schema := plan.QuerySchema{{Usage: integer.Usage()}}
	left := &exec.Source{Anonymous: [][]sqltype.Value{{sqltype.Null(integer)}}, Occurrence: 0}
	right := &exec.Source{Anonymous: [][]sqltype.Value{{sqltype.Null(integer)}}, Occurrence: 1}
	locks := &exec.LockContext{Leaves: []exec.QueryLockLeaf{{Source: left}, {Source: right}}}
	inputs := &exec.Source{Sources: []*exec.Source{left, right}, Locks: locks}
	join := &plan.JoinNode{QuerySchema: append(append(plan.QuerySchema(nil), schema...), schema...), Kind: parser.JoinFull, Left: &plan.SourceNode{QuerySchema: schema, Occurrence: 0}, Right: &plan.SourceNode{QuerySchema: schema, Occurrence: 1}, LeftWidth: 1, RightWidth: 1, Predicate: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}
	stage, err := exec.OpenJoinNode(context.Background(), &exec.State{Source: inputs}, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stage.Count() != 2 || len(stage.Source.Provenance) != 4 || len(stage.Source.Versions) != 4 || len(stage.Source.JoinPairs) != 2 {
		t.Fatal(stage.Source)
	}
	for i, pair := range []exec.QueryPair{{Left: 0, Right: -1}, {Left: -1, Right: 0}} {
		if stage.Source.JoinPairs[i] != pair || stage.Source.Provenance[2*i] != pair.Left || stage.Source.Provenance[2*i+1] != pair.Right {
			t.Fatal(stage.Source.Provenance, stage.Source.JoinPairs)
		}
		for side, index := range []int{pair.Left, pair.Right} {
			version := stage.Source.Versions[2*i+side]
			if (version.Values == nil) != (index == exec.AbsentQueryIndex) {
				t.Fatal("real NULL cells confused with absent version", i, side, version)
			}
		}
	}
}

func queryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}

type queryProbeExpression struct {
	expr.Info
	eval func(*exec.Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}

// evalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func evalContext(ctx expr.Context) *exec.Context {
	c, _ := ctx.(*exec.Context)
	return c
}
