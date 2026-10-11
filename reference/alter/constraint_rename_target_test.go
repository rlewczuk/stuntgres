package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConstraintRenameTargetDiagnostics(t *testing.T) {
	constraintRenameScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT old UNIQUE)", 0))
		run(fixture.Exec("CREATE SEQUENCE seq", 0))
		run(fixture.Exec("CREATE VIEW v AS SELECT k FROM t", 0))
		for _, tc := range []struct{ q, code, message string }{
			{"ALTER TABLE absent_schema.t RENAME CONSTRAINT old TO new", "3F000", `schema "absent_schema" does not exist`},
			{"ALTER TABLE seq RENAME CONSTRAINT old TO new", "42809", `cannot rename columns of relation "seq"`},
			{"ALTER TABLE v RENAME CONSTRAINT old TO new", "42704", `constraint "old" for table "v" does not exist`},
			{"ALTER TABLE old RENAME CONSTRAINT old TO new", "42704", `constraint "old" for table "old" does not exist`},
		} {
			for _, prepared := range []bool{false, true} {
				d := constraintRenameError(tc.code, tc.message)
				if tc.q == "ALTER TABLE seq RENAME CONSTRAINT old TO new" {
					d.Detail = "This operation is not supported for sequences."
				}
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='old'", []string{"conname"}, []string{"NAME"}, [][]any{{"old"}}))
			}
		}
	})
}
