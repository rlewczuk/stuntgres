package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertConflictPredicateSuffix(t *testing.T) {
	for _, source := range []string{
		"SELECT k FROM s WHERE active ON CONFLICT(k) DO NOTHING",
		"SELECT k FROM s WHERE active ON /* outer /* nested */ comment */ CONFLICT(k) DO NOTHING",
		"(SELECT k FROM s WHERE active) ON CONFLICT(k) DO NOTHING",
		"SELECT s.k FROM s JOIN other ON conflict WHERE active ON CONFLICT(k) DO NOTHING",
	} {
		t.Run(source, func(t *testing.T) {
			insertConflictSteps(t,
				referenceExec("CREATE TABLE s(k INT,active BOOL)", 0), referenceExec("INSERT INTO s VALUES(1,TRUE),(2,TRUE),(3,FALSE)", 3),
				referenceExec("CREATE TABLE other(conflict BOOL)", 0), referenceExec("INSERT INTO other VALUES(TRUE)", 1),
				referenceExec("CREATE TABLE t(k INT UNIQUE)", 0), referenceExec("INSERT INTO t VALUES(1)", 1),
				referenceExec("INSERT INTO t "+source, 1), insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
		})
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE s(k INT,active BOOL)", 0))
				run(referenceExec("INSERT INTO s VALUES(1,TRUE),(2,TRUE)", 2))
				run(referenceExec("CREATE TABLE t(k INT UNIQUE)", 0))
				run(referenceExec("INSERT INTO t VALUES(1)", 1))
				position := func(q, token string) string {
					return referencePosition(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
				}
				for _, query := range []string{"SELECT k FROM s WHERE active ON CONFLICT", "INSERT INTO t VALUES(9) ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active ON CONFLICT"} {
					diagnostic := controlsReferenceDiagnostic("0A000", "predicate syntax is unsupported")
					diagnostic.Position = position(query, "ON CONFLICT")
					if b.pg {
						diagnostic = controlsReferenceDiagnostic("42601", "syntax error at or near \"ON\"")
						diagnostic.Position = position(query, "ON CONFLICT")
					}
					step := referenceErrorDiagnostic(query, diagnostic)
					step.prepared = prepared
					step.errorStage = "execute"
					if prepared {
						step.errorStage = "prepare"
					}
					run(step)
					run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
				}
				query := "INSERT INTO t VALUES(1) ON \"CONFLICT\" DO NOTHING"
				diagnostic := controlsReferenceDiagnostic("0A000", "INSERT suffix is unsupported")
				diagnostic.Position = position(query, "ON ")
				if b.pg {
					diagnostic = controlsReferenceDiagnostic("42601", "syntax error at or near \"\"CONFLICT\"\"")
					diagnostic.Position = position(query, "\"CONFLICT\"")
				}
				step := referenceErrorDiagnostic(query, diagnostic)
				step.prepared = prepared
				step.errorStage = "execute"
				if prepared {
					step.errorStage = "prepare"
				}
				run(step)
				run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
				if !prepared {
					script := "INSERT INTO t SELECT k FROM s WHERE active ON CONFLICT DO NOTHING; SELECT k FROM s WHERE active"
					if b.pg {
						if _, err := b.db.ExecContext(p.ctx, script); err != nil {
							t.Fatal(err)
						}
						run(insertConflictRows("SELECT k FROM t ORDER BY k", []any{int64(1)}, []any{int64(2)}))
					} else {
						boundary := referenceErrorDiagnostic(script, controlsReferenceDiagnostic("0A000", "multiple statements are unsupported"))
						boundary.errorStage = "execute"
						run(boundary)
						run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
					}
				}
			})
		})
	}
}
