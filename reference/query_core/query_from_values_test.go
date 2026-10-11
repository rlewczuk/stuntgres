package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified contracts validated against pinned PostgreSQL 18.0.
func TestReferenceQueryFromValuesRows(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					cases := []fixture.Step{
						fixture.Select("VALUES (1,'one'),(2,'two'),(1,'one')", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(1), "one"}}),
						fixture.Select("(VALUES (1),(NULL))", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}}),
						fixture.Select("SELECT * FROM (VALUES(1,'a'),(1,'a'),(2,NULL))", []string{"column1", "column2"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}}),
						fixture.Select("SELECT v.*,v.n FROM (VALUES(1,'a')) AS v(n)", []string{"n", "column2", "n"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(1)}}),
						fixture.Select("SELECT * FROM (VALUES(1,2)) v(x,x)", []string{"x", "x"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}),
						fixture.Select("SELECT * FROM (VALUES(1)), (VALUES(2))", []string{"column1", "column1"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}),
						fixture.Select("SELECT v.n FROM (VALUES(1),(1),(2)) v(n) WHERE n=1", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}),
						fixture.Select("SELECT count(*) FROM (VALUES(1),(1),(NULL)) v(n)", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}),
						fixture.Select("SELECT * FROM (VALUES(1),(NULL)) v(n) WHERE FALSE", []string{"n"}, []string{"INT4"}, [][]any{}),
						fixture.Select("SELECT * FROM (VALUES(1),(NULL)) v(n) WHERE NULL", []string{"n"}, []string{"INT4"}, [][]any{}),
					}
					for _, s := range cases {
						s.Prepared, s.Bag = prepared, true
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					for _, s := range []fixture.Step{
						fixture.Select("VALUES(2),(1),(NULL) ORDER BY column1", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}),
						fixture.Select("VALUES(2),(1),(NULL) ORDER BY column1 DESC", []string{"column1"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}}),
						fixture.Select("VALUES(2),(1),(NULL) ORDER BY column1 OFFSET 1 LIMIT 1", []string{"column1"}, []string{"INT4"}, [][]any{{int64(2)}}),
						fixture.Select("VALUES(2),(1),(NULL) LIMIT 0", []string{"column1"}, []string{"INT4"}, [][]any{}),
						fixture.Select("SELECT * FROM (VALUES(2),(1),(NULL) ORDER BY column1 LIMIT 1) v", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}}),
						fixture.Exec("VALUES (1,'one'),(2,'two'),(1,'one')", 3),
					} {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
				})
			}
		})
	}
}
