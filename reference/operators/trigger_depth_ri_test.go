package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDepthRI(t *testing.T) {
	t.Run("cascade", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE td_parent(id integer PRIMARY KEY)",
				"CREATE TABLE td_child(id integer PRIMARY KEY,pid integer REFERENCES td_parent ON UPDATE CASCADE ON DELETE CASCADE)",
				"CREATE TABLE td_ri_log(op text,level text,id integer,d integer)",
				"CREATE TABLE td_ri_when(d integer)",
				"CREATE FUNCTION td_ri_when() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_ri_when VALUES(pg_trigger_depth()); RETURN true; END$$",
				"CREATE FUNCTION td_ri_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_ri_log VALUES(TG_OP,'BEFORE',COALESCE(NEW.id,OLD.id),pg_trigger_depth()); RETURN COALESCE(NEW,OLD); END$$",
				"CREATE FUNCTION td_ri_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_ri_log VALUES(TG_OP,TG_LEVEL,COALESCE(NEW.id,OLD.id,0),pg_trigger_depth()); RETURN NULL; END$$",
				"CREATE TRIGGER b BEFORE UPDATE OR DELETE ON td_child FOR EACH ROW WHEN(td_ri_when()) EXECUTE FUNCTION td_ri_before()",
				"CREATE TRIGGER r AFTER UPDATE OR DELETE ON td_child FOR EACH ROW WHEN(td_ri_when()) EXECUTE FUNCTION td_ri_after()",
				"CREATE TRIGGER s AFTER UPDATE OR DELETE ON td_child WHEN(td_ri_when()) EXECUTE FUNCTION td_ri_after()")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_parent VALUES(1),(2)", 2))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_child VALUES(11,1),(12,1),(21,2)", 3))
			held := depthHeld(t, p, c, "SELECT pg_trigger_depth()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			for _, tc := range []struct{ q, op string }{{"UPDATE td_parent SET id=10 WHERE id=1", "UPDATE"}, {"DELETE FROM td_parent WHERE id=10", "DELETE"}} {
				run(fixture.Exec(tc.q, 1))
				run(fixture.Select("SELECT op,level,id,d FROM td_ri_log ORDER BY level,id", []string{"op", "level", "id", "d"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, [][]any{{tc.op, "BEFORE", int64(11), int64(2)}, {tc.op, "BEFORE", int64(12), int64(2)}, {tc.op, "ROW", int64(11), int64(1)}, {tc.op, "ROW", int64(12), int64(1)}, {tc.op, "STATEMENT", int64(0), int64(1)}}))
				run(fixture.Select("SELECT d FROM td_ri_when ORDER BY d", []string{"d"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}, {int64(1)}}))
				depthReadHeld(t, p, held)
				userOperatorExec(t, p, b, c, "TRUNCATE td_ri_log,td_ri_when")
			}
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION td_ri_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_ri_log VALUES(TG_OP,'FAIL',OLD.id,pg_trigger_depth()); PERFORM 1/0; RETURN OLD; END$$",
				"CREATE TRIGGER z BEFORE DELETE ON td_child FOR EACH ROW EXECUTE FUNCTION td_ri_fail()")
			d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function td_ri_fail() line 1 at PERFORM\nSQL statement \"DELETE FROM ONLY \"public\".\"td_child\" WHERE $1 OPERATOR(pg_catalog.=) \"pid\"\""
			run(fixture.ErrorDiagnostic("DELETE FROM td_parent WHERE id=2", d))
			depthReadHeld(t, p, held)
			run(fixture.Select("SELECT id FROM td_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT id,pid FROM td_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(21), int64(2)}}))
			run(fixture.Select("SELECT d FROM td_ri_when ORDER BY d", []string{"d"}, []string{"INT4"}, nil))
			run(fixture.Select("SELECT d FROM td_ri_log ORDER BY d", []string{"d"}, []string{"INT4"}, nil))
		})
	})
	for _, event := range []string{"DELETE", "UPDATE"} {
		t.Run("default_"+event, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				userOperatorExec(t, p, b, c, "CREATE TABLE td_parent(id integer PRIMARY KEY)", "CREATE TABLE td_child(id integer PRIMARY KEY,pid integer DEFAULT pg_trigger_depth() REFERENCES td_parent ON DELETE SET DEFAULT ON UPDATE SET DEFAULT)")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_parent VALUES(0),(1),(2)", 3))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_child(id) VALUES(10)", 1))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO td_child VALUES(20,2)", 1))
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				q := "DELETE FROM td_parent WHERE id=2"
				if event == "UPDATE" {
					q = "UPDATE td_parent SET id=3 WHERE id=2"
				}
				run(fixture.Exec(q, 1))
				run(fixture.Select("SELECT id,pid FROM td_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(0)}, {int64(20), int64(1)}}))
				run(depthZero())
			})
		})
	}
	t.Run("set_null_and_checks", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE td_parent(id integer PRIMARY KEY)",
				"CREATE TABLE td_child(id integer PRIMARY KEY,pid integer REFERENCES td_parent ON DELETE SET NULL)",
				"CREATE TABLE td_noaction(id integer REFERENCES td_parent)",
				"CREATE TABLE td_check_log(phase integer,d integer)",
				"CREATE FUNCTION td_check_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_check_log VALUES(10,pg_trigger_depth()); RETURN NEW; END$$",
				"CREATE FUNCTION td_check_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_check_log VALUES(20,pg_trigger_depth()); RETURN NULL; END$$",
				"CREATE TRIGGER b BEFORE UPDATE ON td_child FOR EACH ROW EXECUTE FUNCTION td_check_before()",
				"CREATE TRIGGER a AFTER UPDATE ON td_child FOR EACH ROW EXECUTE FUNCTION td_check_after()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("INSERT INTO td_parent VALUES(0),(1),(2)", 3))
			run(fixture.Exec("INSERT INTO td_child VALUES(10,NULL),(11,1)", 2))
			run(fixture.Exec("INSERT INTO td_noaction VALUES(NULL),(0)", 2))
			run(fixture.Exec("UPDATE td_noaction SET id=id", 2))
			run(depthZero())
			run(fixture.Exec("DELETE FROM td_parent WHERE id=2", 1))
			run(depthZero())
			run(fixture.Exec("DELETE FROM td_parent WHERE id=1", 1))
			run(fixture.Select("SELECT phase,d FROM td_check_log ORDER BY phase", []string{"phase", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(2)}, {int64(20), int64(1)}}))
			run(fixture.Select("SELECT id,pid FROM td_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(10), nil}, {int64(11), nil}}))
			userOperatorExec(t, p, b, c, "ALTER TABLE td_noaction DISABLE TRIGGER ALL")
			run(fixture.Exec("INSERT INTO td_noaction VALUES(99)", 1))
			run(depthZero())
			userOperatorExec(t, p, b, c, "ALTER TABLE td_noaction ENABLE TRIGGER ALL")
			run(fixture.Exec("DELETE FROM td_noaction WHERE id=99", 1))
			run(depthZero())
		})
	})
}
