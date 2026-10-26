package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersDiagnostics(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		itSeed(run)
		check := func() {
			s := itProjection(b.PG, "trigger_schema='it_scope'", itSeedRows(database, true))
			run(s)
			run(fixture.Prepared(s))
		}
		check()
		missing := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		missing.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.triggers", missing)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
			check()
			for _, tc := range []struct{ q, code, message string }{
				{"DROP TRIGGER absent ON it_scope.it_data", "42704", `trigger "absent" for table "it_data" does not exist`},
				{"ALTER TRIGGER z ON it_scope.it_data RENAME TO a", "42710", `trigger "a" for relation "it_data" already exists`},
				{"CREATE OR REPLACE TRIGGER z BEFORE UPDATE OF absent ON it_scope.it_data FOR EACH ROW EXECUTE FUNCTION public.it_identity()", "42703", `column "absent" of relation "it_data" does not exist`},
			} {
				s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				check()
			}
		}
		for _, q := range []string{"CREATE ROLE it_reader", "GRANT USAGE ON SCHEMA it_scope TO it_reader", "GRANT TRIGGER ON it_scope.it_data TO it_reader", "REVOKE USAGE ON SCHEMA public FROM PUBLIC", "SET SESSION AUTHORIZATION it_reader"} {
			run(fixture.Exec(q, 0))
		}
		redacted := itSeedRows(database, false)[:4]
		for _, row := range redacted {
			row[9] = "EXECUTE FUNCTION public.it_identity()"
		}
		for _, tc := range []struct{ q, message string }{
			{"DROP TRIGGER z ON it_scope.it_data", "must be owner of relation it_data"},
			{"ALTER TRIGGER z ON it_scope.it_data RENAME TO next", "must be owner of table it_data"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("42501", tc.message))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(itProjection(b.PG, "trigger_schema='it_scope'", redacted))
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		check()
		d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop column v of table it_scope.it_data because other objects depend on it")
		d.Detail = "trigger m on table it_scope.it_data depends on column v of table it_scope.it_data\ntrigger z on table it_scope.it_data depends on column v of table it_scope.it_data"
		d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(fixture.ErrorDiagnostic("ALTER TABLE it_scope.it_data DROP COLUMN v RESTRICT", d))
		check()
	})
}
