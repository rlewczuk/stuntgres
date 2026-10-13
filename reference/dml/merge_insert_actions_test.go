package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertIdentityGenerated(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY,v INT,g INT GENERATED ALWAYS AS(v+1) STORED)", 0))
		for _, q := range []string{
			"MERGE INTO t USING (VALUES(7)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(DEFAULT,s.v,DEFAULT)",
			"MERGE INTO t USING (VALUES(8)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(9)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(id,v) OVERRIDING SYSTEM VALUE VALUES(40,s.v)",
			"MERGE INTO t USING (VALUES(10)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(id,v) OVERRIDING USER VALUE VALUES(500,s.v)",
		} {
			run(fixture.Exec(q, 1))
		}
		run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(8)}, {int64(2), int64(8), int64(9)}, {int64(3), int64(10), int64(11)}, {int64(40), int64(9), int64(10)}}))
		for _, q := range []string{
			"MERGE INTO t USING (SELECT 11 AS v WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(11)) s(v) ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(7)) s(v) ON t.v=s.v WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
		} {
			run(fixture.Exec(q, 0))
		}
		// Preparing and suppressed arms have not consumed an identity draw.
		run(fixture.Exec("MERGE INTO t USING (VALUES(11)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", 1))
		run(fixture.Select("SELECT id,g FROM t WHERE v=11", []string{"id", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(12)}}))
		for _, tc := range []struct {
			column, values string
			d              fixture.Diagnostic
		}{
			{"id", "1", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "id"`, Detail: `Column "id" is an identity column defined as GENERATED ALWAYS.`, Hint: "Use OVERRIDING SYSTEM VALUE to override."}},
			{"g", "1", fixture.Diagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "g"`, Detail: `Column "g" is a generated column.`}},
		} {
			s := fixture.ImplicitError("MERGE INTO t USING (SELECT 1 AS v WHERE FALSE) s ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT("+tc.column+") VALUES("+tc.values+")", tc.d)
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		run(fixture.Exec("CREATE TABLE late(id INT GENERATED ALWAYS AS IDENTITY,v INT NOT NULL)", 0))
		s := fixture.ImplicitError("MERGE INTO late USING (VALUES(1),(NULL::integer)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "late" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "late", Column: "v"})
		s.ErrorStage = "execute"
		run(s)
		run(fixture.Select("SELECT * FROM late", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		run(fixture.Exec("MERGE INTO late USING (VALUES(3)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", 1))
		run(fixture.Select("SELECT * FROM late", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(3)}}))
	})
}

func TestReferenceMergeInsertBoundaries(t *testing.T) {
	for _, tc := range []struct{ suffix, token string }{
		{"WHEN NOT MATCHED THEN INSERT INTO t VALUES(1)", "INTO"},
		{"WHEN NOT MATCHED THEN INSERT SELECT 1", "SELECT"},
		{"WHEN NOT MATCHED THEN INSERT VALUES(1),(2)", ","},
		{"WHEN NOT MATCHED THEN INSERT VALUES()", ")"},
		{"", ""},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON FALSE " + tc.suffix
				d := fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len(q) + 1)}
				if tc.token != "" {
					d.Message = `syntax error at or near "` + tc.token + `"`
					d.Position = fixture.Position(strings.LastIndex(q, tc.token) + 1)
				}
				s := fixture.ImplicitError(q, d)
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
			})
		})
	}
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		run(fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=2", 1))
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	})
	t.Run("bySourceUpdate", func(t *testing.T) {
		mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := fixture.MergeRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED BY SOURCE THEN UPDATE SET id=3", 1))
			run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		})
	})
	for _, tc := range []struct {
		suffix, source string
	}{
		{"WHEN NOT MATCHED THEN DO NOTHING", "(VALUES(2)) s(id)"},
		{"WHEN NOT MATCHED BY TARGET THEN DO NOTHING", "(VALUES(2)) s(id)"},
		{"WHEN MATCHED THEN DO NOTHING", "(VALUES(1)) s(id)"},
		{"WHEN NOT MATCHED BY SOURCE THEN DO NOTHING", "(VALUES(2)) s(id)"},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := fixture.MergeRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE t(id INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
				q := "MERGE INTO t USING " + tc.source + " ON t.id=s.id " + tc.suffix
				run(fixture.Exec(q, 0))
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	t.Run("returning", func(t *testing.T) {
		mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := fixture.MergeRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			q := "MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id) RETURNING t.id"
			s := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}})
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	})
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("CREATE VIEW writable AS SELECT * FROM t", 0))
		q := "MERGE INTO writable USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
		if b.PG {
			run(fixture.Exec(q, 1))
		} else {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "MERGE into views is unsupported"})
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		want := [][]any{}
		if b.PG {
			want = [][]any{{int64(1)}}
		}
		run(fixture.Select("SELECT * FROM t", []string{"id"}, []string{"INT4"}, want))
	})
}
