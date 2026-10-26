package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLSQLExecutionPerformFound(t *testing.T) {
	t.Run("status_order", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE sx_found_rows(i integer)",
				"CREATE PROCEDURE sx_found_child(INOUT x integer) LANGUAGE plpgsql AS $$BEGIN PERFORM 1; x:=x+1; END$$",
				"CREATE FUNCTION sx_found() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text; x integer:=4; BEGIN s:=FOUND::text; PERFORM NULL::integer; s:=s||':'||FOUND; PERFORM 1 WHERE false; s:=s||':'||FOUND; CALL sx_found_child(x); s:=s||':'||FOUND; INSERT INTO sx_found_rows VALUES(1); s:=s||':'||FOUND; x:=x+1; SET application_name='sx_found'; CREATE TABLE sx_found_utility(i integer); s:=s||':'||FOUND; CALL sx_found_child(x); s:=s||':'||FOUND; UPDATE sx_found_rows SET i=2 WHERE i=1; s:=s||':'||FOUND; UPDATE sx_found_rows SET i=3 WHERE i=99; s:=s||':'||FOUND; DELETE FROM sx_found_rows WHERE i=2; s:=s||':'||FOUND; DELETE FROM sx_found_rows WHERE i=2; s:=s||':'||FOUND; RETURN s||':'||x; END$$")
			for _, prepared := range []bool{false, true} {
				s := sqlExecutionStep(b, "SELECT sx_found() AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"false:true:false:false:true:true:true:true:false:true:false:7"}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionCount(t, p, b, c, "sx_found_rows", 0)
				userOperatorExec(t, p, b, c, "DROP TABLE sx_found_utility", "RESET application_name")
			}
			userOperatorExec(t, p, b, c, "CREATE FUNCTION sx_insert_zero() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM 1; INSERT INTO sx_found_rows SELECT 1 WHERE false; RETURN FOUND; END$$")
			sqlExecutionValue(t, p, b, c, "SELECT sx_insert_zero() AS v", "BOOL", false)
			sqlExecutionCount(t, p, b, c, "sx_found_rows", 0)
		})
	})
	t.Run("complete_discard_and_failed_status", func(t *testing.T) {
		sqlExecutionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE sx_perform_seq",
				"CREATE FUNCTION sx_discard() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM nextval('sx_perform_seq') FROM generate_series(1,3); RETURN FOUND; END$$",
				"CREATE FUNCTION sx_failed_status(prior boolean) RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM 1 WHERE prior; BEGIN PERFORM 10/(3-g) FROM generate_series(1,3) AS g; EXCEPTION WHEN division_by_zero THEN RETURN FOUND; END; RETURN NULL; END$$")
			for _, prepared := range []bool{false, true} {
				userOperatorExec(t, p, b, c, "ALTER SEQUENCE sx_perform_seq RESTART WITH 1")
				s := sqlExecutionStep(b, "SELECT sx_discard() AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				sqlExecutionValue(t, p, b, c, "SELECT currval('sx_perform_seq') AS v", "INT8", int64(3))
			}
			for _, prior := range []bool{false, true} {
				sqlExecutionValue(t, p, b, c, "SELECT sx_failed_status($1::boolean) AS v", "BOOL", prior, prior)
			}
		})
	})
}
