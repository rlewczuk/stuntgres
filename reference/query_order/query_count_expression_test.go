package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func countExpressionRegressionSteps() []fixture.Step {
	steps := []fixture.Step{
		fixture.Select("SELECT count(v) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}),
		fixture.Select("SELECT count(*),count(v),count(k),count(NULL) FROM t", []string{"count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(2), int64(0)}}),
		fixture.Select("SELECT count(v),count(v),count(1),count(FALSE),count(''),count(0) FROM t", []string{"count", "count", "count", "count", "count", "count"}, []string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(2), int64(3), int64(3), int64(3), int64(3)}}),
		fixture.Select("SELECT count(v) FROM empty", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(v),count(1) FROM allnull", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(2)}}),
		fixture.Select("SELECT count(v) FROM allnull", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(v) FROM t WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT count(v) FROM t WHERE NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}),
		fixture.Select("SELECT k,count(v) FROM t GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, [][]any{{"a", int64(1)}, {nil, int64(1)}}),
		fixture.Select("SELECT k,count(v) FROM empty GROUP BY k", []string{"k", "count"}, []string{"TEXT", "INT8"}, nil),
		fixture.Select("SELECT k FROM t GROUP BY k HAVING count(v)>0", []string{"k"}, []string{"TEXT"}, [][]any{{"a"}, {nil}}),
		fixture.Select("SELECT count(v+1),count(NULLIF(v,1)) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(1)}}),
		fixture.Select("SELECT count(v IS NULL),count(v>1),count((v>1) OR v IS NULL) FROM t", []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(3)}}),
		fixture.Select("SELECT count(abs(v)) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}),
		fixture.Select("SELECT count(v)+1,COALESCE(count(v),0) FROM t", []string{"?column?", "coalesce"}, []string{"INT8", "INT8"}, [][]any{{int64(3), int64(2)}}),
	}
	for _, arg := range []any{7, nil} {
		n := int64(3)
		if arg == nil {
			n = 0
		}
		steps = append(steps,
			fixture.Select("SELECT count(CAST($1 AS INT)) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{n}}, arg),
			fixture.Select("SELECT count(CAST($1 AS INT)),count($1) FROM t", []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{n, n}}, arg),
		)
	}
	for i := range steps {
		steps[i].Bag = true
	}
	// Exact records validated by the root's pinned PostgreSQL 18.0 checkpoint.
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		query string
		want  fixture.Diagnostic
	}{
		{"SELECT count() FROM t", fixture.Diagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT /*é*/ count() FROM t", fixture.Diagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "14"}},
		{"SELECT count() FROM empty WHERE FALSE", fixture.Diagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT pg_catalog.count() FROM t", fixture.Diagnostic{Code: "42809", Message: "pg_catalog.count(*) must be used to call a parameterless aggregate function", Position: "8"}},
		{"SELECT count(count(*),missing) FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
		{"SELECT count(count(*),1) FROM t", fixture.Diagnostic{Code: "42883", Message: "function count(bigint, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(" + strings.Repeat("1,", 100) + "1) FROM t", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"SELECT count_missing_schema.count(missing) FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "35"}},
		{"SELECT count_missing_schema.count(" + strings.Repeat("1,", 100) + "1) FROM empty", fixture.Diagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"}},
		{"SELECT count_missing_schema.count(1) FROM t WHERE FALSE", fixture.Diagnostic{Code: "3F000", Message: `schema "count_missing_schema" does not exist`, Position: "8"}},
		{"SELECT count_missing_schema.count(" + strings.Repeat("1,", 100) + "missing) FROM empty WHERE FALSE", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "235"}},
		{"SELECT count(1,2) FROM t", fixture.Diagnostic{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(CAST(NULL AS INT),CAST(NULL AS TEXT)) FROM t", fixture.Diagnostic{Code: "42883", Message: "function count(integer, text) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(NULL,'x') FROM t", fixture.Diagnostic{Code: "42883", Message: "function count(unknown, unknown) does not exist", Hint: hint, Position: "8"}},
		{"SELECT pg_catalog.count(1,2) FROM t", fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.count(integer, integer) does not exist", Hint: hint, Position: "8"}},
		{"SELECT count(missing,1) FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT count(missing) FROM t", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "14"}},
		{"SELECT /*é*/ count(missing) FROM empty WHERE FALSE", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "20"}},
		{"SELECT count(count(*)) FROM t", fixture.Diagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"SELECT count(count(v)) FROM empty WHERE FALSE", fixture.Diagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "14"}},
		{"SELECT count(CASE WHEN FALSE THEN count(v) ELSE 1 END) FROM t", fixture.Diagnostic{Code: "42803", Message: "aggregate function calls cannot be nested", Position: "35"}},
		{"SELECT count(v) FROM t WHERE count(v)>0", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in WHERE", Position: "30"}},
		{"SELECT k FROM t GROUP BY count(v)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in GROUP BY", Position: "26"}},
		{"SELECT count(1/0) FROM t", fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
		{"SELECT v FROM t LIMIT count(v)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in LIMIT", Position: "23"}},
		{"SELECT v FROM t OFFSET count(v)", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in OFFSET", Position: "24"}},
		{"SELECT v FROM t FETCH FIRST count(v) ROWS ONLY", fixture.Diagnostic{Code: "42803", Message: "aggregate functions are not allowed in LIMIT", Position: "29"}},
	} {
		step := fixture.ImplicitError(tc.query, tc.want)
		step.SelectRows = true
		steps = append(steps, step)
	}
	for _, arg := range []any{7, nil} {
		for _, tc := range []struct {
			query string
			want  fixture.Diagnostic
		}{
			{"SELECT count($1) FROM t", fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $1"}},
			{"SELECT count($1),count(CAST($1 AS INT)) FROM t", fixture.Diagnostic{Code: "42P08", Message: "could not determine data type of parameter $1", Position: "14"}},
		} {
			step := fixture.ImplicitError(tc.query, tc.want, arg)
			step.SelectRows = true
			steps = append(steps, step)
		}
	}
	return steps
}

func TestReferenceCountExpressionRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0),
		fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3),
		fixture.Exec("CREATE TABLE empty(k TEXT,v INT)", 0),
		fixture.Exec("CREATE TABLE allnull(k TEXT,v INT)", 0),
		fixture.Exec("INSERT INTO allnull VALUES('a',NULL),(NULL,NULL)", 2),
	} {
		p.Step(t, step)
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, step := range countExpressionRegressionSteps() {
				for _, prepared := range []bool{false, true} {
					s := step
					s.Prepared = prepared
					if s.State != "" {
						s.ErrorStage = "execute"
						if prepared && s.State != "22012" {
							s.ErrorStage = "prepare"
						}
						// The original native contract permits either analysis
						// or execution for this constant division. PG executes it.
						if s.State == "22012" && prepared && !backend.PG {
							s.ErrorStage = ""
						}
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
		})
	}
}
