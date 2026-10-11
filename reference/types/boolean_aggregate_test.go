package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func booleanAggregateRegressionSteps() []fixture.Step {
	labels := []string{"bool_and", "bool_or", "every"}
	types := []string{"BOOL", "BOOL", "BOOL"}
	steps := []fixture.Step{
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='T'", labels, types, [][]any{{true, true, true}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='F'", labels, types, [][]any{{false, false, false}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='M'", labels, types, [][]any{{false, true, false}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE k='N'", labels, types, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM empty", labels, types, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE FALSE", labels, types, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b) FROM t WHERE NULL", labels, types, [][]any{{nil, nil, nil}}),
		fixture.Select("SELECT k,bool_and(b),bool_or(b),every(b) FROM t GROUP BY k", []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"T", true, true, true}, {"F", false, false, false}, {"M", false, true, false}, {"N", nil, nil, nil}}),
		fixture.Select("SELECT k,bool_and(b),bool_or(b),every(b) FROM empty GROUP BY k", []string{"k", "bool_and", "bool_or", "every"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, nil),
		fixture.Select("SELECT count(b),bool_and(b),bool_or(b),every(b),bool_and(b) FROM m", []string{"count", "bool_and", "bool_or", "every", "bool_and"}, []string{"INT8", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(2), false, true, false, false}}),
		fixture.Select("SELECT bool_and(b),bool_or(b),every(b),count(b) FROM nn", []string{"bool_and", "bool_or", "every", "count"}, []string{"BOOL", "BOOL", "BOOL", "INT8"}, [][]any{{nil, nil, nil, int64(0)}}),
		fixture.Select("SELECT bool_and('true'),bool_or(NULL) FROM m", []string{"bool_and", "bool_or"}, []string{"BOOL", "BOOL"}, [][]any{{true, nil}}),
	}
	for _, arg := range []any{true, false, nil} {
		steps = append(steps, fixture.Select("SELECT every($1) FROM m", []string{"every"}, []string{"BOOL"}, [][]any{{arg}}, arg))
	}
	for i := range steps {
		steps[i].Bag = true
	}
	// Source-grounded records pending the root's pinned PostgreSQL 18.0 observations.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name := range []string{"bool_and", "bool_or", "every"} {
		for _, tc := range []struct {
			query, signature, position string
		}{
			{"SELECT " + name + "(1) FROM t", name + "(integer)", "8"},
			{"SELECT " + name + "('true'::text) FROM t", name + "(text)", "8"},
			{"SELECT " + name + "(txt) FROM t", name + "(text)", "8"},
			{"SELECT /*é*/ " + name + "(1) FROM empty WHERE FALSE", name + "(integer)", "14"},
			{"SELECT pg_catalog." + name + "(1) FROM t", "pg_catalog." + name + "(integer)", "8"},
			{"SELECT " + name + "() FROM t", name + "()", "8"},
			{"SELECT " + name + "(*) FROM t", name + "()", "8"},
		} {
			step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: tc.position})
			step.SelectRows = true
			steps = append(steps, step)
		}
	}
	for _, tc := range []struct {
		query string
		want  fixture.Diagnostic
	}{
		{"SELECT bool_and('x') FROM t", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "x"`, Position: "17"}},
		{"SELECT bool_and(bool_or(b)) FROM t", fixture.Diagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "17"}},
		{"SELECT bool_and(count(*)) FROM t", fixture.Diagnostic{Code: "42883", Message: "function bool_and(bigint) does not exist", Hint: hint, Position: "8"}},
		{"SELECT b FROM t WHERE bool_and(b)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "23"}},
		{"SELECT b FROM t GROUP BY bool_and(b)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "26"}},
		{"SELECT b,bool_and(b) FROM t", fixture.Diagnostic{Code: "42803", Message: `column "t.b" must appear in the GROUP BY clause or be used in an aggregate function`, Position: "8"}},
	} {
		step := fixture.ImplicitError(tc.query, tc.want)
		step.SelectRows = true
		steps = append(steps, step)
	}
	return steps
}

func TestReferenceBooleanAggregateRegression(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			run := func(t *testing.T, ctx context.Context, x fixture.Executor, r *fixture.Recorder, s fixture.Step) {
				s.NonContext = nonContext
				fixture.RunStep(t, ctx, x, r, s)
			}
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					for _, step := range []fixture.Step{
						fixture.Exec("CREATE TABLE t(k TEXT,b BOOL,v INT,txt TEXT)", 0),
						fixture.Exec("INSERT INTO t VALUES('T',TRUE,1,'true'),('T',TRUE,1,'true'),('T',NULL,1,'true'),('F',FALSE,1,'true'),('F',FALSE,1,'true'),('F',NULL,1,'true'),('M',TRUE,1,'true'),('M',FALSE,1,'true'),('M',NULL,1,'true'),('N',NULL,1,'true'),('N',NULL,1,'true')", 11),
						fixture.Exec("CREATE TABLE empty(k TEXT,b BOOL,v INT,txt TEXT)", 0),
						fixture.Exec("CREATE TABLE m(b BOOL)", 0),
						fixture.Exec("INSERT INTO m VALUES(TRUE),(FALSE),(NULL)", 3),
						fixture.Exec("CREATE TABLE nn(b BOOL NOT NULL)", 0),
					} {
						run(t, p.Ctx, backend.DB, backend.Recorder, step)
					}
					steps := append(booleanAggregateRegressionSteps(),
						fixture.Select("SELECT k FROM t GROUP BY k HAVING bool_or(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}, {"M"}}),
						fixture.Select("SELECT k FROM t GROUP BY k HAVING every(b)", []string{"k"}, []string{"TEXT"}, [][]any{{"T"}}),
						fixture.Select("SELECT COALESCE(bool_and(b),FALSE) AS answer FROM empty", []string{"answer"}, []string{"BOOL"}, [][]any{{false}}),
						fixture.Select("SELECT pg_catalog.bool_and(b),\"bool_or\"(b),every(b IS NULL) FROM m", []string{"bool_and", "bool_or", "every"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false}}),
					)
					for _, step := range steps {
						t.Run(step.Query, func(t *testing.T) {
							if step.State == "" {
								step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
									metadata, err := rows.ColumnTypes()
									if err != nil || len(metadata) != len(step.Types) {
										t.Fatal(metadata, err)
									}
									for i, typ := range metadata {
										isAggregate := step.Types[i] == "BOOL" && (step.Columns[i] == "bool_and" || step.Columns[i] == "bool_or" || step.Columns[i] == "every")
										if step.Types[i] == "BOOL" && typ.ScanType() != reflect.TypeFor[bool]() {
											t.Fatal(i, typ)
										}
										nullable, known := typ.Nullable()
										if backend.PG {
											if nullable || known {
												t.Fatal("pq nullable descriptor", i, nullable, known)
											}
										} else if isAggregate || step.Columns[i] == "count" {
											if !known || nullable != isAggregate {
												t.Fatal("native aggregate nullable", i, nullable, known)
											}
										}
									}
								}
							}
							for _, prepared := range []bool{false, true} {
								step.Prepared, step.Bag = prepared, true
								if step.Diagnostic != nil {
									step.ErrorStage = "execute"
									if prepared {
										step.ErrorStage = "prepare"
									}
								}
								run(t, p.Ctx, backend.DB, backend.Recorder, step)
							}
						})
					}
				})
			}
		})
	}
}
