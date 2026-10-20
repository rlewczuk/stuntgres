package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func queryCreateBoundSource(t *testing.T, e *Engine, width int) (*plan.ProjectNode, *exec.Source) {
	t.Helper()
	schema := make(plan.QuerySchema, width)
	expressions := make([]expr.Expr, width)
	rows := make([][]sqltype.Value, 3)
	for i := range rows {
		rows[i] = make([]sqltype.Value, width)
	}
	for i := range schema {
		usage := testUsage(e, sqltype.Int4)
		schema[i] = catalog.Column{Name: "i", Usage: usage}
		expressions[i] = plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: i}
		for j := range rows {
			rows[j][i], _ = sqltype.Integer(usage.Type, int64(j+1))
		}
	}
	source := &exec.Source{Anonymous: rows}
	return &plan.ProjectNode{QuerySchema: schema, Child: &plan.SourceNode{QuerySchema: schema}, Expressions: expressions, Frame: 1}, source
}

func TestQueryTableCreateDemand(t *testing.T) {
	e := New()
	project, source := queryCreateBoundSource(t, e, 1)
	usage := project.Schema()[0].Usage
	calls, predicates := 0, 0
	project.Child = &plan.FilterNode{QuerySchema: project.Schema(), Child: project.Child, Predicate: queryProbe(testUsage(e, sqltype.Bool), func(c *exec.Context) (sqltype.Value, error) {
		predicates++
		value, _ := c.Row[0].Integer()
		return sqltype.Boolean(testUsage(e, sqltype.Bool).Type, value >= 2), nil
	})}
	project.Expressions = []expr.Expr{queryProbe(usage, func(c *exec.Context) (sqltype.Value, error) { calls++; return c.Row[0], nil })}
	result, count, err := exec.Finalize(context.Background(), project, source, nil)
	if err != nil || count != 2 || calls != 2 || predicates != 3 {
		t.Fatal(result, count, err, calls, predicates)
	}
	finalized := result.(*exec.FinalRows)
	owned := finalized.Values
	rows, err := finalized.TakeTableRows(context.Background())
	if err != nil || len(rows) != 2 || calls != 2 || predicates != 3 || !finalized.Closed || finalized.Values != nil || &rows[0][0] != &owned[0] {
		t.Fatal(rows, err, calls, predicates)
	}
	for i, row := range rows {
		value, _ := row[0].Integer()
		if value != int64(i+2) {
			t.Fatal(row)
		}
	}
	if source.Anonymous != nil {
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
		borrowed := source.Anonymous
		result, count, err := exec.Finalize(context.Background(), project, source, nil)
		if err != nil || count != 3 {
			t.Fatal(result, count, err)
		}
		rows, err := result.(*exec.FinalRows).TakeTableRows(context.Background())
		if err != nil || len(rows) != 3 || source.Anonymous != nil {
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
	result, _, err := exec.Finalize(context.Background(), project, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rows, err := result.(*exec.FinalRows).TakeTableRows(ctx)
	if rows != nil || !errors.Is(err, context.Canceled) || source.Anonymous != nil || !result.(*exec.FinalRows).Closed {
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
		inner := exec.Stage{Source: input, Columns: project.Schema(), Expressions: project.Expressions, Evaluation: exec.Context{EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, Finish: func() error { closes++; return cleanup }}
		if primary {
			inner.Expressions = []expr.Expr{queryProbe(project.Schema()[0].Usage, func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, failure })}
		}
		bridge := &exec.Source{NestedRoot: project, NestedInputs: input, NestedStage: &inner, Materialized: true, FlatWidth: 1}
		outer := &plan.ProjectNode{QuerySchema: project.Schema(), Child: &plan.SourceNode{QuerySchema: project.Schema()}, Expressions: project.Expressions, Frame: 1}
		result, count, err := exec.Finalize(context.Background(), outer, bridge, nil)
		want := cleanup
		if primary {
			want = failure
		}
		if result != nil || count != 0 || err != want || closes != 1 || bridge.NestedStage != nil || input.Anonymous != nil {
			t.Fatal(result, count, err, closes)
		}
		queryCreateAssertAbsent(t, e, d)
		if e.storage.RowFrontier() != 0 || e.storage.VersionFrontier() != 0 || e.visibility.CommitSeq() != 0 {
			t.Fatal("cleanup failure reserved or published data")
		}
	}
}
