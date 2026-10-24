package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func transactionalRoutineDefinitionPrivileges(t *testing.T) {
	for _, family := range []string{"type", "language", "aggregate-support", "operator-support"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/held=%v", family, held), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"txr_creator"})
				routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					defer func() {
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "GRANT USAGE ON LANGUAGE plpgsql TO PUBLIC"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error("privilege cleanup", q, err)
							}
						}
					}()
					routineExec(t, p, b, c, "CREATE ROLE txr_creator", "GRANT USAGE,CREATE ON SCHEMA public TO txr_creator", "CREATE DOMAIN txr_denied_type AS integer", "REVOKE USAGE ON TYPE txr_denied_type FROM PUBLIC", "CREATE FUNCTION txr_private_step(integer,integer) RETURNS integer RETURN $1+$2", "REVOKE EXECUTE ON FUNCTION txr_private_step(integer,integer) FROM PUBLIC")
					q, message := "CREATE FUNCTION txr_denied(x txr_denied_type) RETURNS integer RETURN 1", "permission denied for type txr_denied_type"
					switch family {
					case "language":
						routineExec(t, p, b, c, "REVOKE USAGE ON LANGUAGE plpgsql FROM PUBLIC")
						q, message = "CREATE FUNCTION txr_denied() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 5; END$$", "permission denied for language plpgsql"
					case "aggregate-support":
						q, message = "CREATE AGGREGATE txr_denied(integer) (SFUNC=txr_private_step,STYPE=integer,INITCOND='0')", "permission denied for function txr_private_step"
					case "operator-support":
						q, message = "CREATE OPERATOR public.#%# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=txr_private_step,COMMUTATOR=OPERATOR(public.#@#))", "permission denied for function txr_private_step"
					}
					routineExec(t, p, b, c, "SET SESSION AUTHORIZATION txr_creator", "BEGIN", "SAVEPOINT definition")
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", message))
					s.Prepared, s.ErrorStage = held, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					routineExec(t, p, b, c, "ROLLBACK TO definition")
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname='txr_denied'", 0)
					transactionalRoutineCount(t, p, b, c, "SELECT count(*) AS v FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#%#','#@#')", 0)
					routineExec(t, p, b, c, "RELEASE definition", "COMMIT", "RESET SESSION AUTHORIZATION")
				})
			})
		}
	}
}
