package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeUpdateExcludedAssignments(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,a INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,20)", 1))
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET (a,b)=(t.b,t.a)"
		if b.PG {
			run(fixture.Exec(q, 1))
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(20), int64(10)}}))
		} else {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "row assignments are unsupported", Position: fixture.Position(strings.Index(q, "(a,b)") + 1)})
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
			run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(20)}}))
		}
	})
}

func TestReferenceMergeUpdateMalformedActions(t *testing.T) {
	for _, tc := range []struct{ name, suffix, token string }{
		{"actionTable", "t SET a=1", "t SET"},
		{"actionWhere", "SET a=1 WHERE TRUE", "WHERE"},
		{"actionFrom", "SET a=1 FROM s", "FROM s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE " + tc.suffix
				token := strings.Fields(tc.token)[0]
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: fixture.Position(strings.LastIndex(q, tc.token) + 1)})
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
			})
		})
	}
}
