package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDepthEvent(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			eventTriggerScenario(t, []string{"td_event"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE td_event_log(d integer)",
					"CREATE TABLE td_event_source(id integer)",
					"CREATE FUNCTION td_event_helper() RETURNS integer LANGUAGE SQL AS 'SELECT pg_trigger_depth()'",
					"CREATE FUNCTION td_event_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_event_log VALUES(td_event_helper()); END$$",
					"CREATE FUNCTION td_event_ddl() RETURNS integer LANGUAGE SQL AS 'CREATE TABLE td_event_nested(id integer); SELECT 1'",
					"CREATE FUNCTION td_event_dml() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM td_event_ddl(); RETURN NEW; END$$",
					"CREATE TRIGGER td_event_dml BEFORE INSERT ON td_event_source FOR EACH ROW EXECUTE FUNCTION td_event_dml()",
				} {
					run(fixture.Exec(q, 0))
				}
				eventTriggerCreate(t, p, b, c, "td_event", "ddl_command_start", "'CREATE TABLE'", "td_event_fn")
				s := fixture.Exec("CREATE TABLE td_event_top(id integer)", 0)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT d FROM td_event_log ORDER BY d", []string{"d"}, []string{"INT4"}, [][]any{{int64(0)}}))
				s = fixture.Exec("INSERT INTO td_event_source VALUES(1)", 1)
				s.Prepared = prepared
				run(s)
				run(fixture.Select("SELECT d FROM td_event_log ORDER BY d", []string{"d"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
				run(fixture.Select("SELECT pg_trigger_depth()", []string{"pg_trigger_depth"}, []string{"INT4"}, [][]any{{int64(0)}}))
			})
		})
	}
}
