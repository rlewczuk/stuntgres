package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAnyValueRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE t(k TEXT,v INTEGER,b BOOL)", 0),
				fixture.Exec("CREATE TABLE empty(k TEXT,v INTEGER,b BOOL)", 0),
				fixture.Exec("INSERT INTO t VALUES('one',NULL,NULL),('one',41,FALSE),('one',NULL,NULL),('repeat',41,FALSE),('repeat',41,FALSE),('n',NULL,NULL),('n',NULL,NULL),('a',NULL,NULL),('a',11,TRUE),('a',23,FALSE),('a',11,TRUE),(NULL,7,TRUE)", 12),
			} {
				run(s)
			}
			for _, q := range []string{"SELECT any_value(v) FROM empty", "SELECT any_value(v) FROM t WHERE FALSE", "SELECT any_value(v) FROM t WHERE NULL", "SELECT any_value(v) FROM t WHERE k='n'", "SELECT any_value(NULL::integer) FROM t"} {
				fixture.AnyValueRoutes(t, p, backend, fixture.Select(q, []string{"any_value"}, []string{"INT4"}, [][]any{{nil}}))
			}
			for _, k := range []string{"one", "repeat"} {
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT any_value(v) FROM t WHERE k='"+k+"'", []string{"any_value"}, []string{"INT4"}, [][]any{{int64(41)}}))
			}
			fixture.AnyValueRoutes(t, p, backend,
				fixture.Select("SELECT any_value(1) FROM t", []string{"any_value"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT k,any_value(v) FROM empty GROUP BY k", []string{"k", "any_value"}, []string{"TEXT", "INT4"}, nil),
				fixture.Select("SELECT pg_catalog.any_value(v),\"any_value\"(b) AS answer FROM t WHERE k='one'", []string{"any_value", "answer"}, []string{"INT4", "BOOL"}, [][]any{{int64(41), false}}),
			)
			for _, prepared := range []bool{false, true} {
				fixture.AnyValueMembership(t, p, backend, "SELECT any_value(v) FROM t WHERE k='a'", prepared, []string{"any_value"}, []string{"INT4"}, map[any][]any{nil: {int64(11), int64(23)}})
				fixture.AnyValueMembership(t, p, backend, "SELECT k,any_value(v) FROM t GROUP BY k", prepared, []string{"k", "any_value"}, []string{"TEXT", "INT4"}, map[any][]any{"one": {int64(41)}, "repeat": {int64(41)}, "n": nil, "a": {int64(11), int64(23)}, nil: {int64(7)}})
				s := fixture.Select("SELECT any_value(v) FROM t WHERE k='one' OR k='repeat' GROUP BY k", []string{"any_value"}, []string{"INT4"}, [][]any{{int64(41)}, {int64(41)}})
				s.Bag, s.Prepared = true, prepared
				run(s)
			}
			for _, arg := range []any{int64(9), nil} {
				fixture.AnyValueRoutes(t, p, backend, fixture.Select("SELECT any_value($1::integer) FROM t", []string{"any_value"}, []string{"INT4"}, [][]any{{arg}}, arg))
			}
		})
	}
}
