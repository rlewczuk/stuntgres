package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineDefinitionErrors(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				routineExec(t, p, b, c, "BEGIN", "CREATE FUNCTION txr_valid() RETURNS integer RETURN 5", "SAVEPOINT definition")
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				for _, tc := range []struct {
					q string
					d fixture.Diagnostic
				}{
					{"CREATE FUNCTION txr_invalid() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: "return type mismatch in function declared to return integer", Detail: "Actual return type is text.", Context: "SQL function \"txr_invalid\""}},
					{"CREATE FUNCTION txr_invalid(x txr_absent) RETURNS integer RETURN 1", fixture.ControlsReferenceDiagnostic("42704", "type txr_absent does not exist")},
					{"CREATE AGGREGATE txr_invalid(integer) (SFUNC=pg_catalog.int4pl,STYPE=integer,INITCOND='bad')", fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type integer: \"bad\"")},
					{"CREATE OPERATOR public.#%# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=pg_catalog.int4pl,COMMUTATOR=OPERATOR(public.#@#),HASHES)", fixture.ControlsReferenceDiagnostic("42P13", "only boolean operators can hash")},
				} {
					if strings.Contains(tc.q, "txr_absent") {
						tc.d.Position = fixture.Position(strings.Index(tc.q, "txr_absent") + 1)
					}
					s := fixture.ErrorDiagnostic(tc.q, tc.d)
					s.Prepared, s.ErrorStage = held, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					routineExec(t, p, b, c, "ROLLBACK TO definition")
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_invalid'", 0)
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#%#','#@#')", 0)
					routineValue(t, p, b, c, "SELECT txr_valid() AS v", "v", "INT4", int64(5))
				}
				routineExec(t, p, b, c, "RELEASE definition", "COMMIT")
				routineValue(t, p, b, c, "SELECT txr_valid() AS v", "v", "INT4", int64(5))
			})
		})
	}
}
