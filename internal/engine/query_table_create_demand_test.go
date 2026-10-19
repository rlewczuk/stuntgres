package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func queryCreateBoundSource(t *testing.T, e *Engine, width int) (*projectNode, *querySource) {
	t.Helper()
	schema := make(querySchema, width)
	expressions := make([]expr.Expr, width)
	rows := make([][]sqltype.Value, 3)
	for i := range rows {
		rows[i] = make([]sqltype.Value, width)
	}
	for i := range schema {
		usage := testUsage(e, sqltype.Int4)
		schema[i] = catalog.Column{Name: "i", Usage: usage}
		expressions[i] = columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: i}
		for j := range rows {
			rows[j][i], _ = sqltype.Integer(usage.Type, int64(j+1))
		}
	}
	source := &querySource{anonymous: rows}
	return &projectNode{querySchema: schema, child: &sourceNode{querySchema: schema}, expressions: expressions, frame: 1}, source
}

func TestQueryTableCreateDemand(t *testing.T) {
	e := New()
	project, source := queryCreateBoundSource(t, e, 1)
	usage := project.Schema()[0].Usage
	calls, predicates := 0, 0
	project.child = &filterNode{querySchema: project.Schema(), child: project.child, predicate: queryProbe(testUsage(e, sqltype.Bool), func(c *EvalContext) (sqltype.Value, error) {
		predicates++
		value, _ := c.Row[0].Integer()
		return sqltype.Boolean(testUsage(e, sqltype.Bool).Type, value >= 2), nil
	})}
	project.expressions = []expr.Expr{queryProbe(usage, func(c *EvalContext) (sqltype.Value, error) { calls++; return c.Row[0], nil })}
	result, count, err := finalizeQuery(context.Background(), project, source, nil)
	if err != nil || count != 2 || calls != 2 || predicates != 3 {
		t.Fatal(result, count, err, calls, predicates)
	}
	finalized := result.(*finalRows)
	owned := finalized.values
	rows, err := finalized.takeTableRows(context.Background())
	if err != nil || len(rows) != 2 || calls != 2 || predicates != 3 || !finalized.closed || finalized.values != nil || &rows[0][0] != &owned[0] {
		t.Fatal(rows, err, calls, predicates)
	}
	for i, row := range rows {
		value, _ := row[0].Integer()
		if value != int64(i+2) {
			t.Fatal(row)
		}
	}
	if source.anonymous != nil {
		t.Fatal("source owner retained")
	}
	stage, d := queryCreateStage(t, e, "copy", 1)
	if err = e.stageQueryTableRows(context.Background(), stage, d, rows, ids.TxRef{}); err != nil {
		t.Fatal(err)
	}
	queryCreatePublish(t, e, stage)
	if calls != 2 || predicates != 3 || len(e.storage.Relations[d.Address()].Versions) != 2 {
		t.Fatal("publication demanded query again")
	}
}

func TestQueryTableCreateOwnedDetach(t *testing.T) {
	for _, width := range []int{0, 1} {
		e := New()
		project, source := queryCreateBoundSource(t, e, width)
		borrowed := source.anonymous
		result, count, err := finalizeQuery(context.Background(), project, source, nil)
		if err != nil || count != 3 {
			t.Fatal(result, count, err)
		}
		rows, err := result.(*finalRows).takeTableRows(context.Background())
		if err != nil || len(rows) != 3 || source.anonymous != nil {
			t.Fatal(rows, err)
		}
		for _, row := range rows {
			if len(row) != width {
				t.Fatal(row)
			}
		}
		if width == 1 {
			if &rows[0][0] == &borrowed[0][0] {
				t.Fatal("borrowed slot storage aliased")
			}
			borrowed[0][0], _ = sqltype.Integer(rows[0][0].Type(), 99)
			value, _ := rows[0][0].Integer()
			if value != 1 {
				t.Fatal("source scratch mutation changed detached datum")
			}
		}
	}
	e := New()
	project, source := queryCreateBoundSource(t, e, 1)
	result, _, err := finalizeQuery(context.Background(), project, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := result.(*finalRows).takeTableRows(ctx)
	if rows != nil || !errors.Is(err, context.Canceled) || source.anonymous != nil || !result.(*finalRows).closed {
		t.Fatal(rows, err)
	}
}

func TestQueryTableCreateCleanup(t *testing.T) {
	for _, primary := range []bool{false, true} {
		e := New()
		project, input := queryCreateBoundSource(t, e, 1)
		stage, d := queryCreateStage(t, e, "copy", 1)
		defer e.DiscardCatalogStage(stage)
		cleanup := errors.New("required source close")
		failure := errors.New("primary expression failure")
		closes := 0
		inner := indexedStage{source: input, schema: project.Schema(), expressions: project.expressions, evaluation: EvalContext{Scope: 1, Frame: 1}, finish: func() error { closes++; return cleanup }}
		if primary {
			inner.expressions = []expr.Expr{queryProbe(project.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, failure })}
		}
		bridge := &querySource{nestedRoot: project, nestedInputs: input, nestedStage: &inner, materialized: true, flatWidth: 1}
		outer := &projectNode{querySchema: project.Schema(), child: &sourceNode{querySchema: project.Schema()}, expressions: project.expressions, frame: 1}
		result, count, err := finalizeQuery(context.Background(), outer, bridge, nil)
		want := cleanup
		if primary {
			want = failure
		}
		if result != nil || count != 0 || err != want || closes != 1 || bridge.nestedStage != nil || input.anonymous != nil {
			t.Fatal(result, count, err, closes)
		}
		queryCreateAssertAbsent(t, e, d)
		if e.storage.RowFrontier() != 0 || e.storage.VersionFrontier() != 0 || e.visibility.CommitSeq() != 0 {
			t.Fatal("cleanup failure reserved or published data")
		}
	}
}
