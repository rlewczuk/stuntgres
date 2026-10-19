package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectRevokeFeatureSmoke(t *testing.T) {
	objectGrantScenario(t, []string{"or_smoke"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
		run(fixture.Exec("CREATE SCHEMA or_smoke", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA or_smoke TO og_object_reader,og_object_other", 0))
		run(fixture.Exec("CREATE SEQUENCE or_smoke.s START 100", 0))
		run(fixture.Exec("GRANT USAGE ON SEQUENCE or_smoke.s TO og_object_reader WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT USAGE ON SEQUENCE or_smoke.s TO og_object_other", 0)) })
		seqSQL := "SELECT nextval('or_smoke.s') AS v"
		absSQL := "SELECT abs($1::integer) AS v"
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader WITH GRANT OPTION", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_other", 0)) })
		var seq, abs *sql.Stmt
		fixture.RoleGrantActor(run, "og_object_other", func() {
			var err error
			seq, err = c.PrepareContext(p.Ctx, seqSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if abs == nil {
					seq.Close()
				}
			}()
			abs, err = c.PrepareContext(p.Ctx, absSQL)
			if err != nil {
				t.Fatal(err)
			}
		})
		defer seq.Close()
		defer abs.Close()
		held := func(stmt *sql.Stmt, s fixture.Step) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
		}
		next := func(value int64) {
			held(seq, fixture.Select(seqSQL, []string{"v"}, []string{"INT8"}, [][]any{{value}}))
		}
		absolute := func() {
			s := fixture.Select(absSQL, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
			s.Args = []any{int64(-7)}
			held(abs, s)
		}
		denied := func(stmt *sql.Stmt, query, message string, args ...any) {
			s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: message})
			s.Args = args
			s.ErrorStage = "execute"
			held(stmt, s)
		}
		fixture.RoleGrantActor(run, "og_object_other", func() { next(100); absolute() })
		for _, suffix := range []string{"", " RESTRICT"} {
			s := fixture.ImplicitError("REVOKE GRANT OPTION FOR USAGE ON SEQUENCE or_smoke.s FROM og_object_reader"+suffix, fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
			s.ErrorStage = "execute"
			run(s)
		}
		fixture.RoleGrantActor(run, "og_object_other", func() { next(101) })
		run(fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON SEQUENCE or_smoke.s FROM og_object_reader CASCADE", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { denied(seq, seqSQL, "permission denied for sequence s") })
		fixture.RoleGrantActor(run, "og_object_reader", func() { next(102) })
		run(fixture.Exec("GRANT USAGE ON SEQUENCE or_smoke.s TO PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { next(103) })
		run(fixture.Exec("REVOKE USAGE ON SEQUENCE or_smoke.s FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { denied(seq, seqSQL, "permission denied for sequence s") })
		run(fixture.Select("SELECT last_value,is_called FROM or_smoke.s", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(103), true}}))
		s := fixture.ImplicitError("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
		s.ErrorStage = "execute"
		run(s)
		fixture.RoleGrantActor(run, "og_object_other", absolute)
		run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader CASCADE", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { denied(abs, absSQL, "permission denied for function abs", int64(-7)) })
		fixture.RoleGrantActor(run, "og_object_reader", absolute)
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", absolute)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_other", 0))
		fixture.RoleGrantActor(run, "og_object_other", func() { denied(abs, absSQL, "permission denied for function abs", int64(-7)) })
	})
}
