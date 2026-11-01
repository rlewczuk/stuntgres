package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoElementTypesDiagnostics(t *testing.T) {
	etScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, database string) {
		etExec(t, p, b, c, "CREATE TABLE et_diagnostic(a integer[])")
		valid := etProjection(b.PG, "object_schema='public' AND object_name='et_diagnostic'", [][]any{etRow(database, "public", "et_diagnostic", "TABLE", 1, "integer", "pg_catalog", "int4")})
		held := etHeld(t, p, b, c, valid.Query)
		held(valid)
		// Positions are character counts derived independently from the SQL, not
		// observations of native failures. All unspecified diagnostic fields are empty.
		for _, x := range []struct{ query, state, message, position string }{
			{"SELECT absent FROM information_schema.element_types", "42703", `column "absent" does not exist`, "8"},
			{"SELECT p.object_name FROM information_schema.element_types q", "42P01", `missing FROM-clause entry for table "p"`, "8"},
			{"SELECT NULL::et_missing_type FROM information_schema.element_types", "42704", `type "et_missing_type" does not exist`, "14"},
			{"SELECT * FROM information_schema.element_types WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "54"},
			{"/*é*/ SELECT absent FROM information_schema.element_types", "42703", `column "absent" does not exist`, "14"},
		} {
			d := fixture.ControlsReferenceDiagnostic(x.state, x.message)
			d.Position = x.position
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(x.query, d)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				held(valid)
			}
			etCheck(t, p, b, c, valid)
		}
	})
}
