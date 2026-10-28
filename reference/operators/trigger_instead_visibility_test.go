package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func insteadVisibility(t *testing.T) {
	t.Helper()
	t.Run("volatile-and-null-statement", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TABLE io_seen(id integer,n bigint)", "CREATE FUNCTION io_seen() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_seen VALUES(NEW.id,(SELECT count(*) FROM io_base)); INSERT INTO io_base VALUES(NEW.id,NEW.v); RETURN NEW; END$$", "CREATE FUNCTION io_statement() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_seen VALUES(99,(SELECT count(*) FROM io_base)); RETURN NULL; END$$", "CREATE OR REPLACE TRIGGER body INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_seen()", "CREATE TRIGGER statement AFTER INSERT ON io_view EXECUTE FUNCTION io_statement()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO io_view VALUES(1,10),(2,20)", 2))
			run(fixture.Select("SELECT n FROM io_seen WHERE id<>99 ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}, {int64(1)}}))
			run(fixture.Select("SELECT id,n FROM io_seen WHERE id=99", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(2)}}))
			run(fixture.Exec("DELETE FROM io_seen", 3))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION io_seen() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$")
			run(triggerRows("INSERT INTO io_view VALUES(3,30),(4,40) RETURNING id,v"))
			run(fixture.Exec("INSERT INTO io_view VALUES(5,50)", 0))
			run(fixture.Select("SELECT id,n FROM io_seen", []string{"id", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(99), int64(2)}, {int64(99), int64(2)}}))
			run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
		})
	})
	for _, volatility := range []string{"STABLE", "IMMUTABLE"} {
		t.Run(volatility, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE FUNCTION io_snapshot() RETURNS bigint LANGUAGE SQL "+volatility+" AS 'SELECT count(*) FROM io_base'", "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_base VALUES(NEW.id,NEW.v); INSERT INTO io_log VALUES('snapshot',NEW.id,NULL,io_snapshot()::integer); RETURN NEW; END$$")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				run(fixture.Exec("INSERT INTO io_view VALUES(1,10),(2,20)", 2))
				first, second := int64(1), int64(2)
				// IMMUTABLE permits constant folding against the initial empty source;
				// STABLE reads each ordinary nested command's invocation frontier.
				if volatility == "IMMUTABLE" {
					first, second = 0, 0
				}
				run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"snapshot", int64(1), nil, first}, []any{"snapshot", int64(2), nil, second}))
			})
		})
	}
}
