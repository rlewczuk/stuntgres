package writes_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryLimitInsertDefaultDemand(t *testing.T) {
	for _, count := range []int64{0, 1} {
		t.Run(map[int64]string{0: "zero", 1: "one"}[count], func(t *testing.T) {
			e := engine.New()
			query, source := enginetest.QueryFixture(t, e, "SELECT id FROM t")
			enginetest.Run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
			ref := enginetest.RoleTableRef(t, e, "d")
			relation := e.CatalogRead().MustRelation(ref.Address)
			project := query.Root.(*plan.ProjectNode)
			project.Child = &plan.LimitNode{QuerySchema: project.Child.Schema(), Child: project.Child, Offset: queryLimitBound(t, e, 1), Count: queryLimitBound(t, e, count)}
			typ := relation.Columns[1].Usage.Type
			value, _ := sqltype.Integer(typ, 7)
			calls := 0
			probe := enginetest.QueryProbe(relation.Columns[1].Usage, func(*exec.Context) (sqltype.Value, error) {
				calls++
				return value, nil
			})
			bound := analyze.Binding{Relation: relation, Query: query, Targets: []int{0}, Insert: analyze.InsertBinding{Defaults: []expr.Expr{nil, probe}}}
			rows, err := dml.EvaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil, exec.DefaultOccurrence{})
			if err != nil || len(rows.Values) != int(count) || calls != int(count) || source.View != nil {
				t.Fatal(rows, err, calls, source.View)
			}
			if count != 0 {
				id, _ := rows.Values[0][0].Integer()
				n, _ := rows.Values[0][1].Integer()
				if id != 2 || n != 7 {
					t.Fatal(id, n)
				}
			}
		})
	}
}

func TestQueryLimitInsertDefaultFailure(t *testing.T) {
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, "SELECT id FROM t")
	enginetest.Run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
	ref := enginetest.RoleTableRef(t, e, "d")
	relation := e.CatalogRead().MustRelation(ref.Address)
	project := query.Root.(*plan.ProjectNode)
	project.Child = &plan.LimitNode{QuerySchema: project.Child.Schema(), Child: project.Child, Count: queryLimitBound(t, e, 2)}
	value, _ := sqltype.Integer(relation.Columns[1].Usage.Type, 7)
	failure := errors.New("retained runtime default failure")
	calls := 0
	probe := enginetest.QueryProbe(relation.Columns[1].Usage, func(*exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			return sqltype.Value{}, failure
		}
		return value, nil
	})
	bound := analyze.Binding{Relation: relation, Query: query, Targets: []int{0}, Insert: analyze.InsertBinding{Defaults: []expr.Expr{nil, probe}}}
	rows, err := dml.EvaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil, exec.DefaultOccurrence{})
	// One destination row was assembled before the next retained default
	// failed. Neither that successful prefix nor the source pin may escape.
	if rows.Values != nil || err != failure || calls != 2 || source.View != nil || source.Committed != nil || source.Own != nil {
		t.Fatal(rows, err, calls, source.View, source.Committed, source.Own)
	}
}

func TestInsertSelectDefaultOccurrencePropagation(t *testing.T) {
	const sql = "INSERT INTO d(id) SELECT id FROM t"
	statements := []parser.Statement{enginetest.Statement(t, sql), enginetest.Statement(t, sql)}
	if statements[0] == statements[1] {
		t.Fatal("separate parses must retain distinct original statements")
	}
	for original, statement := range statements {
		for _, mode := range []string{"zero", "many", "late-error", "shared-zero"} {
			t.Run(map[int]string{0: "first", 1: "second"}[original]+"/"+mode, func(t *testing.T) {
				e := engine.New()
				query, source := enginetest.QueryFixture(t, e, "SELECT id FROM t")
				enginetest.Run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
				ref := enginetest.RoleTableRef(t, e, "d")
				relation := e.CatalogRead().MustRelation(ref.Address)
				project := query.Root.(*plan.ProjectNode)
				count := int64(2)
				if mode == "zero" {
					count = 0
				}
				project.Child = &plan.LimitNode{QuerySchema: project.Child.Schema(), Child: project.Child, Count: queryLimitBound(t, e, count)}
				occurrence := exec.DefaultOccurrence{Statement: statement, Expansion: 41 + original}
				if mode == "shared-zero" {
					occurrence = exec.DefaultOccurrence{}
				}
				defaultNode, origin := analyze.ColumnDefault(e.CatalogRead(), relation, 1)
				// A default origin owns local subkeys, not the command key.
				// Even text-identical foreign syntax must not replace it.
				origin.Occurrence = exec.DefaultOccurrence{Statement: statements[1-original], Expansion: 99, Kind: 2, Row: 3, Slot: 1}
				want := occurrence
				want.Kind, want.Row, want.Slot = 2, 3, 1
				failure := errors.New("later occurrence default failure")
				calls := 0
				probe := enginetest.QueryProbe(relation.Columns[1].Usage, func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if c.DefaultOccurrence != want || c.AcceptedDefault != nil {
						t.Fatalf("default occurrence=%+v accepted=%v, want=%+v without accepted artifact", c.DefaultOccurrence, c.AcceptedDefault, want)
					}
					if mode == "late-error" && calls == 2 {
						return sqltype.Value{}, failure
					}
					return defaultNode.Eval(c)
				})
				bound := analyze.Binding{Relation: relation, Ref: ref, Query: query, Targets: []int{0}, Insert: analyze.InsertBinding{
					Defaults: []expr.Expr{nil, probe}, DefaultOrigins: []analyze.DefaultOrigin{{}, origin},
				}}
				var rows index.WriteBatch
				var err error
				if mode == "shared-zero" {
					state := &exec.State{Source: source, SourceMode: exec.SourceReusable}
					state.CapturePlannerAdmission()
					defer state.ReleaseMemberships()
					stage, openErr := exec.OpenQueryProjectStage(context.Background(), state, project)
					if openErr != nil {
						t.Fatal(openErr)
					}
					rows, err = dml.AssembleInsertRows(context.Background(), bound, &stage, -1, "public", state.Input, state.Output, nil, false, nil, state, exec.DefaultOccurrence{})
					source.Close()
				} else {
					rows, err = dml.EvaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil, occurrence)
				}
				if source.View != nil || source.Committed != nil || source.Own != nil {
					t.Fatal("source pin escaped query assembly", source)
				}
				if mode == "late-error" {
					if err != failure || rows.Values != nil || calls != 2 {
						t.Fatal("failed default exposed a partial batch", rows, err, calls)
					}
					return
				}
				if err != nil || len(rows.Values) != int(count) || calls != int(count) {
					t.Fatal(rows, err, calls)
				}
				for _, row := range rows.Values {
					value, err := row[1].Integer()
					if err != nil || value != 7 {
						t.Fatal("omitted default was not consumed", row, err)
					}
				}
			})
		}
	}
}

func queryLimitBound(t *testing.T, e *core.Engine, value int64) expr.Expr {
	t.Helper()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int8)
	v, err := sqltype.Integer(typ, value)
	if err != nil {
		t.Fatal(err)
	}
	return enginetest.QueryProbe(typ.Usage(), func(*exec.Context) (sqltype.Value, error) { return v, nil })
}
