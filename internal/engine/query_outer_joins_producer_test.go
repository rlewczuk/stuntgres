package engine

import (
	"context"
	"errors"
	"runtime"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryOuterJoinsZeroWidthOccurrences(t *testing.T) {
	e := New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		for _, truth := range []bool{false, true} {
			join := &joinNode{kind: kind, left: &sourceNode{occurrence: 0}, right: &sourceNode{occurrence: 1}, predicate: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, truth)}}
			source := &querySource{sources: []*querySource{{anonymous: [][]sqltype.Value{{}, {}}}, {anonymous: [][]sqltype.Value{{}, {}, {}}}}}
			stage, err := openJoinNode(context.Background(), &queryState{source: source}, join)
			if err != nil {
				t.Fatal(err)
			}
			if err := stage.drain(context.Background()); err != nil {
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
			if stage.count() != want || stage.source.flatCount != want || len(stage.source.flat) != 0 {
				t.Fatal(kind, truth, stage.count(), stage.source)
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
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	value, _ := sqltype.Integer(integer, 7)
	schema := querySchema{{Name: "k", Usage: integer.Usage()}}
	calls := 0
	missing := &joinNode{querySchema: schema, left: &sourceNode{occurrence: 1}, right: &sourceNode{occurrence: 2}, merges: []expr.Expr{queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) {
		calls++
		return sqltype.Value{}, errors.New("missing recipe evaluated")
	})}}
	join := &joinNode{querySchema: append(append(querySchema(nil), schema...), schema...), kind: parser.JoinLeft, left: &sourceNode{querySchema: schema, occurrence: 0}, right: missing, leftWidth: 1, rightWidth: 1, predicate: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, true)}}
	source := &querySource{sources: []*querySource{{anonymous: [][]sqltype.Value{{value}}}, {anonymous: [][]sqltype.Value{}}, {anonymous: [][]sqltype.Value{}}}}
	stage, err := openJoinNode(context.Background(), &queryState{source: source}, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	row := stage.source.row(stage.index(0))
	if stage.count() != 1 || calls != 0 || len(row) != 2 || !row[1].IsNull() || row[1].Type() != integer {
		t.Fatal(calls, row)
	}
}

func TestQueryOuterJoinsRejectedProductAllocation(t *testing.T) {
	e := New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	// Retained frames/flags may grow with demanded child and emitted occurrences,
	// never with the rejected pair matrix. Zero-width isolates that storage.
	allocated := func(size int) uint64 {
		inputs := make([][]sqltype.Value, size)
		for i := range inputs {
			inputs[i] = []sqltype.Value{}
		}
		source := &querySource{sources: []*querySource{{anonymous: inputs}, {anonymous: inputs}}}
		join := &joinNode{kind: parser.JoinFull, left: &sourceNode{occurrence: 0}, right: &sourceNode{occurrence: 1}, predicate: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		stage, err := openJoinNode(context.Background(), &queryState{source: source}, join)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.drain(context.Background()); err != nil {
			t.Fatal(err)
		}
		if stage.count() != 2*size {
			t.Fatal(stage.count())
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
	e := New()
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		closed := [2]int{}
		closeFailure := errors.New("child close")
		primary := errors.New("predicate primary")
		bridge := func(which int) *querySource {
			inner := &indexedStage{source: &querySource{anonymous: [][]sqltype.Value{{}}}, evaluation: EvalContext{}, finish: func() error { closed[which]++; return closeFailure }}
			return &querySource{materialized: true, nestedRoot: &projectNode{child: &singletonNode{}}, nestedStage: inner}
		}
		inputs := &querySource{sources: []*querySource{bridge(0), bridge(1)}}
		join := &joinNode{kind: kind, left: &sourceNode{occurrence: 0}, right: &sourceNode{occurrence: 1}, predicate: queryProbe(boolean.Usage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, primary })}
		rows, _, err := finalizeQuery(context.Background(), &projectNode{child: join}, inputs, nil)
		if rows != nil || !errors.Is(err, primary) || closed != [2]int{1, 1} {
			t.Fatal(kind, rows, err, closed)
		}
	}
}

func TestQueryOuterJoinsNullRowsAreNotAbsentOccurrences(t *testing.T) {
	e := New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	schema := querySchema{{Usage: integer.Usage()}}
	left := &querySource{anonymous: [][]sqltype.Value{{sqltype.Null(integer)}}, occurrence: 0}
	right := &querySource{anonymous: [][]sqltype.Value{{sqltype.Null(integer)}}, occurrence: 1}
	locks := &queryLockContext{leaves: []queryLockLeaf{{source: left}, {source: right}}}
	inputs := &querySource{sources: []*querySource{left, right}, locks: locks}
	join := &joinNode{querySchema: append(append(querySchema(nil), schema...), schema...), kind: parser.JoinFull, left: &sourceNode{querySchema: schema, occurrence: 0}, right: &sourceNode{querySchema: schema, occurrence: 1}, leftWidth: 1, rightWidth: 1, predicate: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}}
	stage, err := openJoinNode(context.Background(), &queryState{source: inputs}, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	if err := stage.drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stage.count() != 2 || len(stage.source.provenance) != 4 || len(stage.source.versions) != 4 || len(stage.source.joinPairs) != 2 {
		t.Fatal(stage.source)
	}
	for i, pair := range []queryPair{{left: 0, right: -1}, {left: -1, right: 0}} {
		if stage.source.joinPairs[i] != pair || stage.source.provenance[2*i] != pair.left || stage.source.provenance[2*i+1] != pair.right {
			t.Fatal(stage.source.provenance, stage.source.joinPairs)
		}
		for side, index := range []int{pair.left, pair.right} {
			version := stage.source.versions[2*i+side]
			if (version.values == nil) != (index == absentQueryIndex) {
				t.Fatal("real NULL cells confused with absent version", i, side, version)
			}
		}
	}
}
