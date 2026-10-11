package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexRegression(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "nonContext"}[nonContext], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					bag := func(query string, rows [][]any) fixture.Step {
						s := fixture.Select(query, []string{"n", "s"}, []string{"INT4", "TEXT"}, rows)
						s.Bag = true
						return s
					}
					steps := []fixture.Step{
						fixture.Exec("CREATE TABLE t(n INTEGER,s TEXT)", 0),
						fixture.Exec("INSERT INTO t VALUES(2,'b'),(1,'a'),(2,'b'),(NULL,NULL),(3,'c')", 5),
						fixture.Exec("CREATE INDEX t_n ON t(n)", 0),
						fixture.Prepared(fixture.Exec("CREATE INDEX t_s ON t USING btree(s)", 0)),
						bag("SELECT n,s FROM t WHERE n=2", [][]any{{int64(2), "b"}, {int64(2), "b"}}),
						bag("SELECT n,s FROM t WHERE n>=2", [][]any{{int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}}),
						bag("SELECT n,s FROM t WHERE n IS NULL", [][]any{{nil, nil}}),
						bag("SELECT n,s FROM t WHERE s>'a'", [][]any{{int64(2), "b"}, {int64(3), "c"}, {int64(2), "b"}}),
						fixture.Select("SELECT n,s FROM t ORDER BY n ASC NULLS LAST", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(2), "b"}, {int64(3), "c"}, {nil, nil}}),
						fixture.Select("SELECT n,s FROM t ORDER BY n DESC", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}, {int64(3), "c"}, {int64(2), "b"}, {int64(2), "b"}, {int64(1), "a"}}),
					}
					parameter := bag("SELECT n,s FROM t WHERE n=$1", [][]any{{int64(2), "b"}, {int64(2), "b"}})
					parameter.Args = []any{2}
					steps = append(steps, fixture.Prepared(parameter))
					for _, tc := range []struct{ query, code, message string }{
						{"CREATE INDEX t_n ON t(n)", "42P07", `relation "t_n" already exists`},
						{"CREATE INDEX retry ON missing(n)", "42P01", `relation "missing" does not exist`},
						{"CREATE INDEX retry ON t(missing)", "42703", `column "missing" does not exist`},
					} {
						s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
						s.ErrorStage = "execute"
						steps = append(steps, s, fixture.Prepared(s))
					}
					steps = append(steps, fixture.Exec("CREATE INDEX retry ON t(n)", 0))
					for _, step := range steps {
						step.NonContext = nonContext
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
						if step.SelectRows && !step.Prepared {
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
						}
					}
				})
			}
		})
	}
}
