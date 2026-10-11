package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAnyValueSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(k TEXT,v INT,b BOOL,i INTERVAL)", 0), fixture.Exec("CREATE TABLE empty(k TEXT,v INT,b BOOL,i INTERVAL)", 0),
				fixture.Exec("INSERT INTO t VALUES('one',NULL,NULL,NULL),('one',41,FALSE,'1 day'),('one',NULL,NULL,NULL),('a',11,TRUE,NULL),('a',23,FALSE,NULL),('a',11,TRUE,NULL),(NULL,7,TRUE,NULL),('n',NULL,NULL,NULL)", 8),
				fixture.Exec("CREATE TABLE out(k TEXT,v INT)", 0), fixture.Exec("CREATE TABLE d(v INT)", 0), fixture.Exec("INSERT INTO d VALUES(1),(0)", 2),
			} {
				run(s)
			}
			fixture.AnyValueRoutes(t, p, backend,
				fixture.Select("SELECT any_value(v) FROM empty", []string{"any_value"}, []string{"INT4"}, [][]any{{nil}}),
				fixture.Select("SELECT any_value(v) FROM t WHERE k='n'", []string{"any_value"}, []string{"INT4"}, [][]any{{nil}}),
				fixture.Select("SELECT any_value(v) FROM t WHERE k='one'", []string{"any_value"}, []string{"INT4"}, [][]any{{int64(41)}}),
				fixture.Select("SELECT any_value(v),count(v),bool_or(b),avg(i) FROM t WHERE k='one'", []string{"any_value", "count", "bool_or", "avg"}, []string{"INT4", "INT8", "BOOL", "INTERVAL"}, [][]any{{int64(41), int64(1), false, "1 day"}}),
				fixture.Select("SELECT COALESCE(any_value(v),0) AS answer FROM empty", []string{"answer"}, []string{"INT4"}, [][]any{{int64(0)}}),
				fixture.Select("SELECT k FROM t WHERE k='one' GROUP BY k HAVING any_value(v)=41 ORDER BY any_value(v)", []string{"k"}, []string{"TEXT"}, [][]any{{"one"}}),
			)
			for _, arg := range []any{int64(9), nil} {
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT any_value($1::integer) FROM t", []string{"any_value"}, []string{"INT4"}, [][]any{{arg}}, arg))
			}
			for _, prepared := range []bool{false, true} {
				fixture.AnyValueMembership(t, p, backend, "SELECT any_value(v) FROM t WHERE k='a'", prepared, []string{"any_value"}, []string{"INT4"}, map[any][]any{nil: {int64(11), int64(23)}})
				fixture.AnyValueMembership(t, p, backend, "SELECT k,any_value(v) FROM t WHERE k='a' OR k='n' OR k IS NULL GROUP BY k", prepared, []string{"k", "any_value"}, []string{"TEXT", "INT4"}, map[any][]any{"a": {int64(11), int64(23)}, "n": nil, nil: {int64(7)}})
				for _, s := range []fixture.Step{fixture.Exec("INSERT INTO out(v) SELECT any_value(v) FROM empty", 1), fixture.Exec("INSERT INTO out SELECT k,any_value(v) FROM empty GROUP BY k", 0), fixture.Exec("INSERT INTO out SELECT k,any_value(v) FROM t WHERE k='one' GROUP BY k", 1)} {
					s.Prepared = prepared
					run(s)
				}
				for _, q := range []string{"SELECT any_value($1::integer/v) FROM d", "INSERT INTO out(v) SELECT any_value($1::integer/v) FROM d"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"}, 10)
					s.SelectRows, s.Prepared, s.ErrorStage = q[:6] == "SELECT", prepared, "execute"
					run(s)
				}
			}
			s := fixture.Select("SELECT k,v FROM out", []string{"k", "v"}, []string{"TEXT", "INT4"}, [][]any{{nil, nil}, {"one", int64(41)}, {nil, nil}, {"one", int64(41)}})
			s.Bag = true
			run(s)
			anyValueViewLifecycle(t, p, backend)
		})
	}
}
