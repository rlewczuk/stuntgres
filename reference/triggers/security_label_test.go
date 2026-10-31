package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSecurityLabelEventBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_sl_start", "et_sl_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var notices []fixture.Diagnostic
				pool, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				defer pool.Close()
				defer c.Close()
				b.Recorder = recorder
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
				failure := func(q string, diagnostic fixture.Diagnostic, wantNotices []fixture.Diagnostic) {
					t.Helper()
					notices = nil
					s := fixture.ErrorDiagnostic(q, diagnostic)
					s.ErrorStage = "execute"
					if prepared {
						statement, err := c.PrepareContext(p.Ctx, q)
						if err != nil { t.Fatal("utility preparation", q, err) }
						defer statement.Close()
						fixture.RegtypeAssertNotices(t, notices, nil)
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}, recorder, s)
					} else {
						run(s)
					}
					fixture.RegtypeAssertNotices(t, notices, wantNotices)
				}
				run(fixture.Exec("CREATE FUNCTION et_sl_start() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'security label start guard'; END$$", 0))
				eventTriggerCreate(t, p, b, c, "et_sl_start", "ddl_command_start", "'SECURITY LABEL'", "et_sl_start")
				for _, target := range []string{"TABLE sl_missing", "SUBSCRIPTION sl_missing"} {
					failure("SECURITY LABEL ON "+target+" IS NULL", eventTriggerFailure("et_sl_start", "security label start guard"), nil)
				}
				for _, target := range []string{"DATABASE sl_missing", "ROLE sl_missing", "TABLESPACE sl_missing", "EVENT TRIGGER sl_missing"} {
					failure("SECURITY LABEL ON "+target+" IS NULL", eventTriggerDiagnostic("22023", "no security label providers have been loaded"), nil)
				}
				run(fixture.Exec("CREATE OR REPLACE FUNCTION et_sl_start() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'start:%',TG_TAG; END$$", 0))
				run(fixture.Exec("CREATE FUNCTION et_sl_end() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE NOTICE 'end:%',TG_TAG; END$$", 0))
				eventTriggerCreate(t, p, b, c, "et_sl_end", "ddl_command_end", "'SECURITY LABEL'", "et_sl_end")
				// Notices survive statement rollback, unlike a transactional audit row.
				for _, target := range []string{"TABLE sl_missing", "SUBSCRIPTION sl_missing"} {
					failure("SECURITY LABEL ON "+target+" IS NULL", eventTriggerDiagnostic("22023", "no security label providers have been loaded"), []fixture.Diagnostic{eventTriggerNotice("et_sl_start", "start:SECURITY LABEL")})
				}
			})
		})
	}
}
