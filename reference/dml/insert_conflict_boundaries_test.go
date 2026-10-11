package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictNamedLifecycle(t *testing.T) {
	fixture.UniqueReferenceSchemaCleanup(t, "conflict_moved")
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		q := "INSERT INTO t VALUES($1) ON CONFLICT ON CONSTRAINT u DO NOTHING"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, int64(1)))
		run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT u", 0))
		d := fixture.ControlsReferenceDiagnostic("42704", "constraint \"u\" for table \"t\" does not exist")
		s := fixture.ErrorDiagnostic(q, d, int64(1))
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("DROP TABLE t", 0))
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, int64(1)))
		run(fixture.Exec("CREATE SCHEMA conflict_moved", 0))
		run(fixture.Exec("SET search_path=conflict_moved,public", 0))
		run(fixture.Exec("ALTER TABLE t SET SCHEMA conflict_moved", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0, int64(1)))
		run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
		run(fixture.Exec("ALTER TABLE t SET SCHEMA public", 0))
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("DROP SCHEMA conflict_moved", 0))
	})
}
func TestReferenceInsertConflictExcludedActions(t *testing.T) {
	for _, suffix := range []string{"ON CONFLICT(k) DO UPDATE SET p='changed'", "ON CONFLICT(k) DO NOTHING RETURNING k"} {
		for _, prepared := range []bool{false, true} {
			t.Run(suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,p TEXT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'kept')", 1))
					q := "INSERT INTO t VALUES(1,'ignored') " + suffix
					if strings.Contains(suffix, "RETURNING") {
						s := fixture.Select(q, []string{"k"}, []string{"INT4"}, nil)
						s.Prepared = prepared
						run(s)
					} else {
						s := fixture.Exec(q, 1)
						s.Prepared = prepared
						run(s)
					}
					payload := "kept"
					if !strings.Contains(suffix, "RETURNING") {
						payload = "changed"
					}
					run(fixture.Select("SELECT k,p FROM t", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), payload}}))
				})
			})
		}
	}
}
func TestReferenceInsertConflictDefaultFailure(t *testing.T) {
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE,p INT DEFAULT 1/0)", 0), fixture.Exec("INSERT INTO t VALUES(1,1)", 1),
		fixture.ErrorDiagnostic("INSERT INTO t(k) VALUES(1) ON CONFLICT DO NOTHING", fixture.ControlsReferenceDiagnostic("22012", "division by zero")), insertConflictRows("SELECT k FROM t", []any{int64(1)}))
}
