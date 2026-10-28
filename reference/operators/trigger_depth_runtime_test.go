package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func depthSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE td_outer(id integer PRIMARY KEY,d integer DEFAULT pg_trigger_depth())",
		"CREATE TABLE td_inner(id integer PRIMARY KEY,d integer DEFAULT pg_trigger_depth())",
		"CREATE TABLE td_log(id integer,phase integer,d integer)",
		"CREATE FUNCTION td_sql() RETURNS integer LANGUAGE SQL AS 'SELECT pg_trigger_depth()'",
		"CREATE FUNCTION td_helper() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN td_sql(); END$$",
		"CREATE FUNCTION td_inner_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(NEW.id,20,td_helper()); RETURN NEW; END$$",
		"CREATE FUNCTION td_outer_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(NEW.id,10,td_helper()); INSERT INTO td_inner(id) VALUES(NEW.id); INSERT INTO td_log VALUES(NEW.id,30,td_helper()); RETURN NEW; END$$",
		"CREATE TRIGGER inner_depth BEFORE INSERT ON td_inner FOR EACH ROW WHEN(pg_trigger_depth()=1) EXECUTE FUNCTION td_inner_fn()",
		"CREATE TRIGGER outer_depth BEFORE INSERT ON td_outer FOR EACH ROW WHEN(pg_trigger_depth()=0) EXECUTE FUNCTION td_outer_fn()")
}

func depthLog(q string, rows ...[]any) fixture.Step {
	return fixture.Select(q, []string{"id", "phase", "d"}, []string{"INT4", "INT4", "INT4"}, rows)
}

func TestReferenceTriggerDepthNesting(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		depthSetup(t, p, b, c)
		held := depthHeld(t, p, c, "SELECT td_helper()")
		depthReadHeld(t, p, held)
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO td_outer(id) VALUES(1)", 1))
		run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase", []any{int64(1), int64(10), int64(1)}, []any{int64(1), int64(20), int64(2)}, []any{int64(1), int64(30), int64(1)}))
		run(fixture.Select("SELECT id,d FROM td_outer ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}}))
		run(fixture.Select("SELECT id,d FROM td_inner ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
		depthReadHeld(t, p, held)
		run(depthZero())
	})
}

func depthUncaughtDiagnostic() fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
	d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function td_fail() line 1 at PERFORM\nSQL statement \"INSERT INTO td_inner(id) VALUES(NEW.id)\"\nPL/pgSQL function td_outer_fn() line 1 at SQL statement"
	return d
}

func TestReferenceTriggerDepthUnwind(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		depthSetup(t, p, b, c)
		held := depthHeld(t, p, c, "SELECT pg_trigger_depth()")
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION td_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(NEW.id,21,pg_trigger_depth()); PERFORM 1/0; RETURN NEW; END$$",
			"CREATE OR REPLACE TRIGGER inner_depth BEFORE INSERT ON td_inner FOR EACH ROW EXECUTE FUNCTION td_fail()",
			"CREATE FUNCTION td_catch() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(NEW.id,10,pg_trigger_depth()); BEGIN INSERT INTO td_inner(id) VALUES(NEW.id); EXCEPTION WHEN division_by_zero THEN INSERT INTO td_log VALUES(NEW.id,30,pg_trigger_depth()); END; RETURN NEW; END$$",
			"CREATE OR REPLACE TRIGGER outer_depth BEFORE INSERT ON td_outer FOR EACH ROW EXECUTE FUNCTION td_catch()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO td_outer(id) VALUES(1)", 1))
		run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase", []any{int64(1), int64(10), int64(1)}, []any{int64(1), int64(30), int64(1)}))
		run(fixture.Select("SELECT id FROM td_inner ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
		depthReadHeld(t, p, held)
		userOperatorExec(t, p, b, c, "CREATE OR REPLACE TRIGGER outer_depth BEFORE INSERT ON td_outer FOR EACH ROW EXECUTE FUNCTION td_outer_fn()")
		for _, explicit := range []bool{false, true} {
			if explicit {
				run(fixture.Exec("BEGIN", 0))
			}
			run(fixture.ErrorDiagnostic("INSERT INTO td_outer(id) VALUES(2)", depthUncaughtDiagnostic()))
			if explicit {
				run(fixture.Exec("ROLLBACK", 0))
			}
			depthReadHeld(t, p, held)
			run(fixture.Select("SELECT id FROM td_outer ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT id FROM td_inner ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
			run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase", []any{int64(1), int64(10), int64(1)}, []any{int64(1), int64(30), int64(1)}))
		}
	})
}

func TestReferenceTriggerDepthTiming(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		depthSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION td_sibling() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(NEW.id,40,pg_trigger_depth()); RETURN NEW; END$$",
			"CREATE FUNCTION td_skip() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE TRIGGER a_skip BEFORE INSERT ON td_outer FOR EACH ROW WHEN(NEW.id=9 AND pg_trigger_depth()=0) EXECUTE FUNCTION td_skip()",
			"CREATE TRIGGER z_sibling BEFORE INSERT ON td_outer FOR EACH ROW EXECUTE FUNCTION td_sibling()",
			"CREATE TRIGGER zz_false BEFORE INSERT ON td_outer FOR EACH ROW WHEN(false) EXECUTE FUNCTION td_sibling()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO td_outer(id) VALUES(1),(2),(9)", 2))
		var want [][]any
		for _, id := range []int64{1, 2} {
			for _, phase := range []int64{10, 20, 30, 40} {
				d := int64(1)
				if phase == 20 {
					d = 2
				}
				want = append(want, []any{id, phase, d})
			}
		}
		run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase", want...))
		run(depthZero())
	})
}

func TestReferenceTriggerDepthAfter(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		depthSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION td_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO td_log VALUES(COALESCE(NEW.id,0),CASE WHEN TG_LEVEL='ROW' THEN 50 ELSE 60 END,pg_trigger_depth()); RETURN NULL; END$$",
			"CREATE TRIGGER after_row AFTER INSERT ON td_outer FOR EACH ROW WHEN(pg_trigger_depth()=0) EXECUTE FUNCTION td_after()",
			"CREATE TRIGGER after_stmt AFTER INSERT ON td_outer EXECUTE FUNCTION td_after()",
			"CREATE TRIGGER after_row AFTER INSERT ON td_inner FOR EACH ROW WHEN(pg_trigger_depth()=1) EXECUTE FUNCTION td_after()",
			"CREATE TRIGGER after_stmt AFTER INSERT ON td_inner EXECUTE FUNCTION td_after()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO td_outer(id) VALUES(1)", 1))
		run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase,d", []any{int64(0), int64(60), int64(1)}, []any{int64(0), int64(60), int64(2)}, []any{int64(1), int64(10), int64(1)}, []any{int64(1), int64(20), int64(2)}, []any{int64(1), int64(30), int64(1)}, []any{int64(1), int64(50), int64(1)}, []any{int64(1), int64(50), int64(2)}))
		run(depthZero())
		userOperatorExec(t, p, b, c,
			"TRUNCATE td_log",
			"CREATE TABLE td_cte(id integer,d integer DEFAULT pg_trigger_depth())",
			"CREATE TRIGGER r AFTER INSERT ON td_cte FOR EACH ROW WHEN(pg_trigger_depth()=0) EXECUTE FUNCTION td_after()",
			"CREATE TRIGGER s AFTER INSERT ON td_cte WHEN(pg_trigger_depth()=0) EXECUTE FUNCTION td_after()")
		run(fixture.Select("WITH x AS (INSERT INTO td_cte(id) VALUES(1) RETURNING id), y AS (INSERT INTO td_cte(id) VALUES(2) RETURNING id) SELECT x.id AS x,y.id AS y,pg_trigger_depth() AS d FROM x,y", []string{"x", "y", "d"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(0)}}))
		run(depthLog("SELECT id,phase,d FROM td_log ORDER BY id,phase", []any{int64(0), int64(60), int64(1)}, []any{int64(1), int64(50), int64(1)}, []any{int64(2), int64(50), int64(1)}))
		run(fixture.Select("SELECT id,d FROM td_cte ORDER BY id", []string{"id", "d"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(0)}}))
		run(depthZero())
	})
}
