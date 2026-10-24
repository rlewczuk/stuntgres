package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutinePrivateEffects(t *testing.T) {
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c, "CREATE TABLE txr_effect_rows(v integer)", "CREATE TABLE txr_error_row(v integer)", "CREATE SEQUENCE txr_effect_seq")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO txr_error_row VALUES(0)", 1))
		routineExec(t, p, b, c, "BEGIN", "CREATE FUNCTION txr_effect(integer) RETURNS integer LANGUAGE SQL AS $$INSERT INTO txr_effect_rows VALUES($1); SELECT $1$$", "CREATE FUNCTION txr_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('txr_effect_seq')$$")
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_effect_rows", 0)
		routineValue(t, p, b, c, "SELECT txr_effect(4) AS v", "v", "INT4", int64(4))
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_effect_rows", 2)
		// Unreached volatile/body failures must not become planning effects.
		routineValue(t, p, b, c, "SELECT CASE WHEN false THEN txr_effect(99) ELSE 5 END AS v", "v", "INT4", int64(5))
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_effect_rows", 2)
		for _, held := range []bool{false, true} {
			routineExec(t, p, b, c, "SAVEPOINT effects")
			q := "SELECT txr_effect(20),10/v FROM txr_error_row"
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
			s.SelectRows, s.Prepared, s.ErrorStage = true, held, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			routineExec(t, p, b, c, "ROLLBACK TO effects", "RELEASE effects")
			transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_effect_rows", 2)
		}
		// Sequence advancement is explicitly outside row/catalog rollback.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT txr_next() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		routineExec(t, p, b, c, "SAVEPOINT sequence_mark")
		q := "SELECT txr_next(),10/v FROM txr_error_row"
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		routineExec(t, p, b, c, "ROLLBACK TO sequence_mark", "RELEASE sequence_mark")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT txr_next() AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(3)}}))
		routineExec(t, p, b, c, "ROLLBACK")
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM txr_effect_rows", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT nextval('txr_effect_seq') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(4)}}))
		transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('txr_effect','txr_next')", 0)
	})
}
