package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow per-trigger TriggerEnabled and completion-time statement
// event admission, not the role at entry to the row chain or producer.
func TestReferenceTriggerModeActivation(t *testing.T) {
	for _, transition := range []struct{ from, to string; value int64 }{
		{"origin", "replica", 1005}, {"replica", "origin", 105},
	} {
		for _, route := range []string{"direct", "held"} {
			t.Run(transition.from+"-to-"+transition.to+"/"+route, func(t *testing.T) {
				triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					userOperatorExec(t, p, b, c,
						"CREATE TABLE tm_switch(id integer,v integer)",
						"CREATE TABLE tm_switch_audit(label text)",
						"CREATE FUNCTION tm_switch_role() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM set_config('session_replication_role',TG_ARGV[0],false); RETURN NEW; END$$",
						"CREATE FUNCTION tm_switch_origin() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$",
						"CREATE FUNCTION tm_switch_replica() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1000; RETURN NEW; END$$",
						"CREATE FUNCTION tm_switch_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tm_switch_audit VALUES(TG_ARGV[0]); RETURN NULL; END$$",
						"CREATE TRIGGER a BEFORE INSERT ON tm_switch FOR EACH ROW EXECUTE FUNCTION tm_switch_role('"+transition.to+"')",
						"CREATE TRIGGER b BEFORE INSERT ON tm_switch FOR EACH ROW EXECUTE FUNCTION tm_switch_origin()",
						"CREATE TRIGGER c BEFORE INSERT ON tm_switch FOR EACH ROW EXECUTE FUNCTION tm_switch_replica()",
						"CREATE TRIGGER row_origin AFTER INSERT ON tm_switch FOR EACH ROW EXECUTE FUNCTION tm_switch_log('row-origin')",
						"CREATE TRIGGER row_replica AFTER INSERT ON tm_switch FOR EACH ROW EXECUTE FUNCTION tm_switch_log('row-replica')",
						// This body runs only after all statement events were admitted.
						// Changing role again must not reselect the following events.
						"CREATE TRIGGER a_reset AFTER INSERT ON tm_switch FOR EACH STATEMENT EXECUTE FUNCTION tm_switch_role('"+transition.from+"')",
						"CREATE TRIGGER statement_origin AFTER INSERT ON tm_switch FOR EACH STATEMENT EXECUTE FUNCTION tm_switch_log('statement-origin')",
						"CREATE TRIGGER statement_replica AFTER INSERT ON tm_switch FOR EACH STATEMENT EXECUTE FUNCTION tm_switch_log('statement-replica')")
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
					q := "INSERT INTO tm_switch VALUES($1,5) RETURNING id,v"
					insert := run
					if route == "held" { insert = triggerDDLHeld(t, p, b, c, q) }
					cte := "WITH inserted AS (INSERT INTO tm_switch VALUES($1,5) RETURNING id,v) SELECT id,v FROM inserted"
					consume := run
					if route == "held" { consume = triggerDDLHeld(t, p, b, c, cte) }
					userOperatorExec(t, p, b, c,
						"ALTER TABLE tm_switch ENABLE ALWAYS TRIGGER a",
						"ALTER TABLE tm_switch ENABLE REPLICA TRIGGER c",
						"ALTER TABLE tm_switch ENABLE REPLICA TRIGGER row_replica",
						"ALTER TABLE tm_switch ENABLE ALWAYS TRIGGER a_reset",
						"ALTER TABLE tm_switch ENABLE REPLICA TRIGGER statement_replica",
						"SET session_replication_role="+transition.from)
					check := func(role string, row bool) {
						labels := [][]any{{"statement-"+role}}
						if row { labels = [][]any{{"row-"+role}, {"statement-"+role}} }
						run(fixture.Select("SELECT label FROM tm_switch_audit ORDER BY label", []string{"label"}, []string{"TEXT"}, labels))
						run(fixture.Select("SELECT current_setting('session_replication_role') AS role", []string{"role"}, []string{"TEXT"}, [][]any{{transition.from}}))
						run(fixture.Exec("DELETE FROM tm_switch_audit", int64(len(labels))))
					}
					s := triggerRows(q, []any{int64(1), transition.value}); s.Args = []any{1}; insert(s)
					check(transition.to, true)
					// A suspended RETURNING modifier retains its own completion boundary.
					s = triggerRows(cte, []any{int64(2), transition.value}); s.Args = []any{2}; consume(s)
					check(transition.to, true)
					// No BEFORE body executes here; statement admission uses the entry role.
					run(fixture.Exec("INSERT INTO tm_switch SELECT 3,5 WHERE false", 0))
					check(transition.from, false)
					run(triggerRows("SELECT id,v FROM tm_switch ORDER BY id", []any{int64(1), transition.value}, []any{int64(2), transition.value}))
					run(fixture.Exec("SET session_replication_role=origin", 0))
				})
			})
		}
	}
}
