package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeAggregateSourceACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ra_reader"})
	rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE ra_reader", 0))
		run(fixture.Exec("CREATE TABLE r(k INT,v INT4RANGE)", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ra_reader", 0))
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION ra_reader", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO r VALUES(1,'[1,4)')", 1))
			}
			for _, otherColumn := range []bool{false, true} {
				if otherColumn {
					run(fixture.Exec("GRANT SELECT(k) ON r TO ra_reader", 0))
				}
				for _, name := range []string{"range_agg", "range_intersect_agg"} {
					for _, q := range []string{"SELECT " + name + "(v) FROM r", "SELECT " + name + "(v) FROM r WHERE FALSE", "SELECT " + name + "(v) FILTER(WHERE FALSE) FROM r"} {
						d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table r")
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic(q, d)
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
						}
					}
				}
				if otherColumn {
					run(fixture.Exec("REVOKE SELECT(k) ON r FROM ra_reader", 0))
				}
			}
		}
		run(fixture.Exec("GRANT SELECT(v) ON r TO ra_reader", 0))
		q := "SELECT range_agg(v),range_intersect_agg(v) FROM r"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		exec := fixture.HeldStatement{conn, stmt, q}
		s := rangeAggregateStep(b.PG, q, []string{"range_agg", "range_intersect_agg"}, []string{"INT4MULTIRANGE", "INT4RANGE"}, [][]any{{"{[1,4)}", "[1,4)"}})
		fixture.RunStep(t, p.Ctx, exec, b.Recorder, s)
		run(fixture.Exec("REVOKE SELECT(v) ON r FROM ra_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table r")
		failure := fixture.ErrorDiagnostic(q, d)
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, exec, b.Recorder, failure)
		run(fixture.Exec("GRANT SELECT(v) ON r TO ra_reader", 0))
		fixture.RunStep(t, p.Ctx, exec, b.Recorder, s)
	})
}
