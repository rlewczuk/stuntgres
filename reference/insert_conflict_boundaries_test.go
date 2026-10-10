package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceInsertConflictNamedLifecycle(t *testing.T) {
	uniqueReferenceSchemaCleanup(t, "conflict_moved")
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		q := "INSERT INTO t VALUES($1) ON CONFLICT ON CONSTRAINT u DO NOTHING"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0, int64(1)))
		run(referenceExec("ALTER TABLE t DROP CONSTRAINT u", 0))
		d := controlsReferenceDiagnostic("42704", "constraint \"u\" for table \"t\" does not exist")
		s := referenceErrorDiagnostic(q, d, int64(1))
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		run(referenceExec("DROP TABLE t", 0))
		run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0, int64(1)))
		run(referenceExec("CREATE SCHEMA conflict_moved", 0))
		run(referenceExec("SET search_path=conflict_moved,public", 0))
		run(referenceExec("ALTER TABLE t SET SCHEMA conflict_moved", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0, int64(1)))
		run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
		run(referenceExec("ALTER TABLE t SET SCHEMA public", 0))
		run(referenceExec("SET search_path=public", 0))
		run(referenceExec("DROP SCHEMA conflict_moved", 0))
	})
}
func TestReferenceInsertConflictExcludedActions(t *testing.T) {
	for _, suffix := range []string{"ON CONFLICT(k) DO UPDATE SET p='changed'", "ON CONFLICT(k) DO NOTHING RETURNING k"} {
		for _, prepared := range []bool{false, true} {
			t.Run(suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(k INT UNIQUE,p TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(1,'kept')", 1))
					q := "INSERT INTO t VALUES(1,'ignored') " + suffix
					if !b.pg {
						token := "UPDATE"
						if strings.Contains(suffix, "RETURNING") {
							token = "RETURNING"
						}
						s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "INSERT suffix is unsupported", Position: referencePosition(strings.Index(q, token) + 1)})
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
					} else if strings.Contains(suffix, "RETURNING") {
						s := referenceSelect(q, []string{"k"}, []string{"INT4"}, nil)
						s.prepared = prepared
						run(s)
					} else {
						s := referenceExec(q, 1)
						s.prepared = prepared
						run(s)
					}
					payload := "kept"
					if b.pg && !strings.Contains(suffix, "RETURNING") {
						payload = "changed"
					}
					run(referenceSelect("SELECT k,p FROM t", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), payload}}))
				})
			})
		}
	}
}
func TestReferenceInsertConflictDefaultFailure(t *testing.T) {
	insertConflictSteps(t, referenceExec("CREATE TABLE t(k INT UNIQUE,p INT DEFAULT 1/0)", 0), referenceExec("INSERT INTO t VALUES(1,1)", 1),
		referenceErrorDiagnostic("INSERT INTO t(k) VALUES(1) ON CONFLICT DO NOTHING", controlsReferenceDiagnostic("22012", "division by zero")), insertConflictRows("SELECT k FROM t", []any{int64(1)}))
}
