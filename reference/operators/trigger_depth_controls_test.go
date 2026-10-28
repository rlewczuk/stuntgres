package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDepthPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "td_saved")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA td_saved",
			"CREATE TABLE td_saved.outer_data(id integer PRIMARY KEY,d integer DEFAULT pg_trigger_depth())",
			"CREATE TABLE td_saved.inner_data(id integer PRIMARY KEY,d integer DEFAULT pg_trigger_depth())",
			"CREATE TABLE td_saved.audit(id integer,phase integer,d integer)",
			"CREATE FUNCTION td_saved.helper() RETURNS integer LANGUAGE SQL AS 'SELECT pg_catalog.pg_trigger_depth()'",
			"CREATE VIEW td_saved.depth AS SELECT pg_trigger_depth() AS d",
			"CREATE FUNCTION td_saved.inner_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_saved.audit VALUES(NEW.id,20,td_saved.helper()); RETURN NEW; END$$",
			"CREATE FUNCTION td_saved.outer_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_saved.audit VALUES(NEW.id,10,td_saved.helper()); INSERT INTO td_saved.inner_data(id) VALUES(NEW.id); INSERT INTO td_saved.audit VALUES(NEW.id,30,td_saved.helper()); RETURN NEW; END$$",
			"CREATE TRIGGER i BEFORE INSERT ON td_saved.inner_data FOR EACH ROW WHEN(pg_trigger_depth()=1) EXECUTE FUNCTION td_saved.inner_fn()",
			"CREATE TRIGGER o BEFORE INSERT ON td_saved.outer_data FOR EACH ROW WHEN(pg_trigger_depth()=0) EXECUTE FUNCTION td_saved.outer_fn()")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_saved.outer_data(id) VALUES(1)", 1))
		held := depthHeld(t, p, c, "SELECT pg_trigger_depth()")
		helper := depthHeld(t, p, c, "SELECT td_saved.helper()")
		q := "INSERT INTO td_saved.outer_data(id) VALUES($1)"
		insert := depthHeld(t, p, c, q)
		check := func(conn *sql.Conn, backend fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(depthZero())
			run(fixture.Select("SELECT d FROM td_saved.depth", []string{"d"}, []string{"INT4"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT id,d FROM td_saved.outer_data ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}))
			run(fixture.Select("SELECT id,d FROM td_saved.inner_data ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
			run(depthLog("SELECT id,phase,d FROM td_saved.audit ORDER BY id,phase", []any{int64(1), int64(10), int64(1)}, []any{int64(1), int64(20), int64(2)}, []any{int64(1), int64(30), int64(1)}))
			if conn == c {
				depthReadHeld(t, p, held)
				depthReadHeld(t, p, helper)
			}
			run(fixture.Exec("BEGIN", 0))
			s := fixture.Exec(q, 1, 99)
			if conn == c {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: insert, SQL: q}, backend.Recorder, s)
			} else {
				run(fixture.Prepared(s))
			}
			run(depthLog("SELECT id,phase,d FROM td_saved.audit WHERE id=99 ORDER BY phase", []any{int64(99), int64(10), int64(1)}, []any{int64(99), int64(20), int64(2)}, []any{int64(99), int64(30), int64(1)}))
			run(fixture.Exec("ROLLBACK", 0))
			if conn == c {
				depthReadHeld(t, p, held)
				depthReadHeld(t, p, helper)
			}
		}
		parametersReplay(t, p, b, c, "td_saved", check)
		if b.PG {
			return
		}
		old, err := c.QueryContext(p.Ctx, "SELECT id,d FROM td_saved.outer_data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"; CREATE TRIGGER bad BEFORE INSERT ON td_saved.outer_data FOR EACH ROW EXECUTE FUNCTION td_missing();")); err == nil {
			t.Fatal("malformed Restore succeeded")
		}
		check(c, b)
		if !old.Next() {
			t.Fatal("saved row lost", old.Err())
		}
		var id, d int64
		if err := old.Scan(&id, &d); err != nil || id != 1 || d != 0 {
			t.Fatal(id, d, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("saved cardinality", old.Err())
		}
	})
}
