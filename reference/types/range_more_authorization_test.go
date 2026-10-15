package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"more_reader"})
	fixture.SchemaReferenceCleanup(t, "more_private")
	dsn := "reference/" + t.Name()
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE USER more_reader", 0))
		run(fixture.Exec("CREATE TABLE more_acl(a int4range,m int4multirange,allowed integer)", 0))
		run(fixture.Exec("GRANT SELECT(allowed) ON more_acl TO more_reader", 0))
		run(fixture.Exec("CREATE SCHEMA more_private", 0))
		run(fixture.Exec("CREATE TABLE more_private.secret(a int4range,m int4multirange)", 0))
		if b.PG {
			run(fixture.Exec("ALTER ROLE more_reader PASSWORD 'role-core-fixture'", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO more_reader", 0))
		}
		actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, dsn, "more_reader")
		if err := actor.PingContext(p.Ctx); err != nil { t.Fatal(err) }
		for _, data := range []string{"", "NULL,NULL", "'empty','{}'", "'[1,4]','{[1,3),[8,9)}'"} {
			if data != "" { run(fixture.Exec("INSERT INTO more_acl(a,m) VALUES ("+data+")", 1)) }
			for _, expr := range []string{"multirange(a)", "unnest(m)"} {
				for _, suffix := range []string{"", " WHERE false", " LIMIT 0"} {
					q := "SELECT "+expr+" AS v FROM more_acl"+suffix
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table more_acl"))
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
					}
				}
			}
			if data != "" { run(fixture.Exec("DELETE FROM more_acl", 1)) }
		}
		for _, expr := range []string{"multirange(a)", "unnest(m)"} {
			q := "SELECT "+expr+" FROM more_private.secret"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, inspectDiagnostic(q, "42501", "permission denied for schema more_private", "", "more_private", "", ""))
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				fixture.RunStep(t, p.Ctx, actor, b.Recorder, s)
			}
		}
		run(fixture.Exec("INSERT INTO more_acl(a,m) VALUES ('[1,4]','{[1,3),[8,9)}')", 1))
		run(fixture.Exec("GRANT SELECT(a,m) ON more_acl TO more_reader", 0))
		for _, expr := range []struct { q, typ string; rows [][]any }{
			{"SELECT multirange(a) AS v FROM more_acl", "INT4MULTIRANGE", [][]any{{"{[1,5)}"}}},
			{"SELECT unnest(m) AS v FROM more_acl", "INT4RANGE", [][]any{{"[1,3)"}, {"[8,9)"}}},
		} {
			stmt, err := actor.PrepareContext(p.Ctx, expr.q)
			if err != nil { t.Fatal(err) }
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: actor, Statement: stmt, SQL: expr.q}
				check := rangeSelect(b, expr.q, []string{"v"}, []string{expr.typ}, expr.rows, nil)
				// RoleScenarioLoginPool is deliberately unwrapped: consume its
				// actual held cursor, not the administrator pool's recorder.
				queryHeld := func() {
					rows, err := stmt.QueryContext(p.Ctx)
					if err != nil { t.Fatal(err) }
					rangeConsumeHeld(t, rows, check)
				}
				queryHeld()
				var retained *sql.Rows
				if !b.PG {
					retained, err = stmt.QueryContext(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer retained.Close()
				}
				run(fixture.Exec("REVOKE SELECT(a,m) ON more_acl FROM more_reader", 0))
				s := fixture.ErrorDiagnostic(expr.q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table more_acl"))
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				if retained != nil { rangeConsumeHeld(t, retained, check) }
				run(fixture.Exec("GRANT SELECT(a,m) ON more_acl TO more_reader", 0))
				queryHeld()
			}()
		}
	})
}
