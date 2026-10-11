package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExpressionIndexRegression(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.NonContext = nonContext
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					for _, s := range []fixture.Step{
						fixture.Exec("CREATE TABLE t(id INT,v TEXT)", 0),
						fixture.Exec("INSERT INTO t VALUES(1,'Alpha'),(2,'ALPHA'),(3,'Beta'),(4,NULL),(5,'alpha')", 5),
						fixture.Exec("CREATE INDEX ix ON t(lower(v))", 0),
						fixture.Prepared(fixture.Exec("CREATE INDEX mixed ON t(id DESC,lower(t.v) ASC NULLS LAST,(id+1),lower(lower(v)))", 0)),
						fixture.Exec("CREATE INDEX ON t(lower(v))", 0),
						fixture.Exec("CREATE INDEX bare_column ON t((v))", 0),
						fixture.Select("SELECT relname FROM pg_class WHERE relname='t_lower_idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"t_lower_idx"}}),
						fixture.Select("SELECT id FROM t WHERE lower(v)='alpha' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(5)}}),
						fixture.Select("SELECT id FROM t WHERE lower(v) IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}),
						fixture.Select("SELECT id,lower(v) AS folded FROM t ORDER BY lower(v),id", []string{"id", "folded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "alpha"}, {int64(2), "alpha"}, {int64(5), "alpha"}, {int64(3), "beta"}, {int64(4), nil}}),
						fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(6,'AlPhA')", 1)),
						fixture.Prepared(fixture.Select("SELECT id FROM t WHERE lower(v)=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(5)}, {int64(6)}}, "alpha")),
						fixture.Exec("CREATE TABLE empty_t(v TEXT)", 0),
						fixture.Exec("CREATE INDEX empty_ix ON empty_t(lower(v))", 0),
						fixture.Select("SELECT lower(v) AS folded FROM empty_t", []string{"folded"}, []string{"TEXT"}, nil),
					} {
						run(s)
					}
				})
			}
		})
	}
}

func TestReferenceExpressionIndexFormerExclusions(t *testing.T) {
	for _, q := range []string{"CREATE INDEX ix ON t((c+1))", "CREATE INDEX ix ON t ((c+1))", "/*é*/ CREATE INDEX ix ON t((c+1))"} {
		for _, nonContext := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(q+map[bool]string{false: "/context", true: "/plain"}[nonContext]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							run := func(s fixture.Step) {
								s.NonContext = nonContext
								fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							}
							run(fixture.Exec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
							run(fixture.Exec("INSERT INTO t VALUES(2,1,'one')", 1))
							s := fixture.Exec(q, 0)
							if prepared {
								s = fixture.Prepared(s)
							}
							run(s)
							run(fixture.Select("SELECT relname,relkind FROM pg_class WHERE relname='ix'", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{"ix", "i"}}))
							run(fixture.Select("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
							run(fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,2,'two')", 1)))
							run(fixture.Select("SELECT c+1 AS next FROM t ORDER BY c", []string{"next"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
							run(fixture.Exec("DELETE FROM t WHERE n=3", 1))
							run(fixture.Exec("ALTER TABLE t ALTER c TYPE BIGINT", 0))
							run(fixture.Select("SELECT c+1 AS next FROM t", []string{"next"}, []string{"INT8"}, [][]any{{int64(2)}}))
						})
					}
				})
			}
		}
	}
}

func TestReferenceExpressionIndexImmutability(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(n INT)", 0))
			run(fixture.Exec("CREATE INDEX dead_case ON t((CASE WHEN FALSE THEN random() ELSE n::double precision END))", 0))
			run(fixture.Prepared(fixture.Exec("CREATE INDEX dead_coalesce ON t((COALESCE(1::double precision,random())))", 0)))
			run(fixture.Exec("CREATE INDEX specialized_concat ON t((n || 'x'))", 0))
			for _, q := range []string{"CREATE INDEX retry ON t(random())", "CREATE INDEX retry ON t((current_user))", "CREATE INDEX retry ON t((CASE WHEN n>0 THEN random() ELSE 1::double precision END))"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("INSERT INTO t VALUES(-2),(NULL)", 2))
			run(fixture.Select("SELECT n || 'x' AS k FROM t ORDER BY n", []string{"k"}, []string{"TEXT"}, [][]any{{"-2x"}, {nil}}))
			run(fixture.Select("SELECT CASE WHEN FALSE THEN random() ELSE n::double precision END AS k FROM t ORDER BY n", []string{"k"}, []string{"FLOAT8"}, [][]any{{float64(-2)}, {nil}}))
			run(fixture.Exec("CREATE INDEX retry ON t((n+1))", 0))
			run(fixture.Exec("CREATE TABLE temporal(d DATE)", 0))
			for _, q := range []string{"CREATE INDEX temporal_stable ON temporal((d || 'x'))", "CREATE INDEX temporal_stable ON temporal((d::text))"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='temporal_stable'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE INDEX temporal_stable ON temporal(d)", 0))
		})
	}
}
