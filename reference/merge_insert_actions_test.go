package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceMergeInsertIdentityGenerated(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
		run := mergeInsertRun(t, p, b, prepared)
		run(referenceExec("CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY,v INT,g INT GENERATED ALWAYS AS(v+1) STORED)", 0))
		for _, q := range []string{
			"MERGE INTO t USING (VALUES(7)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(DEFAULT,s.v,DEFAULT)",
			"MERGE INTO t USING (VALUES(8)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(9)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(id,v) OVERRIDING SYSTEM VALUE VALUES(40,s.v)",
			"MERGE INTO t USING (VALUES(10)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(id,v) OVERRIDING USER VALUE VALUES(500,s.v)",
		} {
			run(referenceExec(q, 1))
		}
		run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(8)}, {int64(2), int64(8), int64(9)}, {int64(3), int64(10), int64(11)}, {int64(40), int64(9), int64(10)}}))
		for _, q := range []string{
			"MERGE INTO t USING (SELECT 11 AS v WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(11)) s(v) ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(v) VALUES(s.v)",
			"MERGE INTO t USING (VALUES(7)) s(v) ON t.v=s.v WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)",
		} {
			run(referenceExec(q, 0))
		}
		// Preparing and suppressed arms have not consumed an identity draw.
		run(referenceExec("MERGE INTO t USING (VALUES(11)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", 1))
		run(referenceSelect("SELECT id,g FROM t WHERE v=11", []string{"id", "g"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(12)}}))
		for _, tc := range []struct {
			column, values string
			d              referenceDiagnostic
		}{
			{"id", "1", referenceDiagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "id"`, Detail: `Column "id" is an identity column defined as GENERATED ALWAYS.`, Hint: "Use OVERRIDING SYSTEM VALUE to override."}},
			{"g", "1", referenceDiagnostic{Code: "428C9", Message: `cannot insert a non-DEFAULT value into column "g"`, Detail: `Column "g" is a generated column.`}},
		} {
			s := referenceImplicitError("MERGE INTO t USING (SELECT 1 AS v WHERE FALSE) s ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT("+tc.column+") VALUES("+tc.values+")", tc.d)
			s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		run(referenceExec("CREATE TABLE late(id INT GENERATED ALWAYS AS IDENTITY,v INT NOT NULL)", 0))
		s := referenceImplicitError("MERGE INTO late USING (VALUES(1),(NULL::integer)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "late" violates not-null constraint`, Detail: "Failing row contains (2, null).", Schema: "public", Table: "late", Column: "v"})
		s.errorStage = "execute"
		run(s)
		run(referenceSelect("SELECT * FROM late", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{}))
		run(referenceExec("MERGE INTO late USING (VALUES(3)) s(v) ON FALSE WHEN NOT MATCHED THEN INSERT(v) VALUES(s.v)", 1))
		run(referenceSelect("SELECT * FROM late", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(3)}}))
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
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, prepared)
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON FALSE " + tc.suffix
				d := referenceDiagnostic{Code: "42601", Message: "syntax error at end of input", Position: referencePosition(len(q) + 1)}
				if tc.token != "" {
					d.Message = `syntax error at or near "` + tc.token + `"`
					d.Position = referencePosition(strings.LastIndex(q, tc.token) + 1)
				}
				s := referenceImplicitError(q, d)
				s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
				run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{}))
			})
		})
	}
	for _, tc := range []struct {
		suffix, token, message string
		rows                   [][]any
		returning              bool
	}{
		{"WHEN NOT MATCHED THEN DO NOTHING", "DO", "MERGE DO NOTHING is unsupported", [][]any{{int64(1)}}, false},
		{"WHEN MATCHED THEN UPDATE SET id=2", "MATCHED", "MERGE MATCHED actions are unsupported", [][]any{{int64(2)}}, false},
		{"WHEN MATCHED THEN DELETE", "MATCHED", "MERGE MATCHED actions are unsupported", [][]any{}, false},
		{"WHEN NOT MATCHED BY SOURCE THEN DELETE", "SOURCE", "MERGE BY SOURCE actions are unsupported", [][]any{}, false},
		{"WHEN NOT MATCHED THEN INSERT VALUES(s.id) RETURNING t.id", "RETURNING", "MERGE RETURNING is unsupported", [][]any{{int64(1)}, {int64(2)}}, true},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
				run := mergeInsertRun(t, p, b, prepared)
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				source := "(VALUES(1)) s(id)"
				if strings.Contains(tc.suffix, "BY SOURCE") || tc.returning {
					source = "(VALUES(2)) s(id)"
				}
				q := "MERGE INTO t USING " + source + " ON t.id=s.id " + tc.suffix
				if b.pg {
					if tc.returning {
						s := referenceSelect(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}})
						s.prepared = prepared
						run(s)
					} else {
						run(referenceExec(q, 0))
					}
				} else {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: referencePosition(strings.LastIndex(q, tc.token) + 1)})
					s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					run(s)
				}
				want := tc.rows
				if !b.pg {
					want = [][]any{{int64(1)}}
				}
				run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, want))
			})
		})
	}
	mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
		run := mergeInsertRun(t, p, b, prepared)
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("CREATE VIEW writable AS SELECT * FROM t", 0))
		q := "MERGE INTO writable USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
		if b.pg {
			run(referenceExec(q, 1))
		} else {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "MERGE into views is unsupported"})
			s.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		want := [][]any{}
		if b.pg {
			want = [][]any{{int64(1)}}
		}
		run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, want))
	})
}
