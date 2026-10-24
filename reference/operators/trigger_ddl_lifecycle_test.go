package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func triggerDDLScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func triggerDDLChain(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	triggerSetup(t, p, b, c)
	userOperatorExec(t, p, b, c,
		"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
		"CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
}

func triggerDDLHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) func(fixture.Step) {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return func(step fixture.Step) {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, b.Recorder, step)
	}
}

func triggerDDLError(q, state, message string, prepared bool) fixture.Step {
	s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(state, message))
	s.ErrorStage = "execute"
	if prepared {
		s = fixture.Prepared(s)
	}
	return s
}

func TestReferenceTriggerDDLRename(t *testing.T) {
	t.Run("chain", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerDDLChain(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			q := "INSERT INTO tr_data VALUES($1,5) RETURNING id,v"
			held := triggerDDLHeld(t, p, b, c, q)
			insert := func(id, value int64) { s := triggerRows(q, []any{id, value}); s.Args = []any{id}; held(s) }
			insert(1, 16)
			run(fixture.Exec(`ALTER TRIGGER z ON public.tr_data RENAME TO "0double"`, 0))
			insert(2, 13)
			run(fixture.Prepared(fixture.Exec(`ALTER TRIGGER "0double" ON tr_data RENAME TO "0double"`, 0)))
			run(triggerDDLError(`ALTER TRIGGER "0double" ON tr_data RENAME TO a`, "42710", `trigger "a" for relation "tr_data" already exists`, false))
			insert(3, 13)
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_other(id integer,v integer)", "CREATE TRIGGER z BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()")
			run(fixture.Prepared(fixture.Exec(`ALTER TRIGGER z ON tr_other RENAME TO "0double"`, 0)))
			run(triggerRows("INSERT INTO tr_other VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			run(fixture.Prepared(triggerRows("INSERT INTO tr_data VALUES(4,5) RETURNING id,v", []any{int64(4), int64(13)})))
			run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(16)}, []any{int64(2), int64(13)}, []any{int64(3), int64(13)}, []any{int64(4), int64(13)}))
		})
	})
	t.Run("rich-definition", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c,
				"CREATE TABLE tr_rich(id integer,v integer,mark text)",
				"CREATE FUNCTION tr_rich_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; NEW.mark:=TG_NAME||':'||TG_ARGV[1]; RETURN NEW; END$$",
				"CREATE TRIGGER old BEFORE UPDATE OF v ON tr_rich FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tr_rich_fn('7','kept')")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("INSERT INTO tr_rich VALUES(1,5,'base'),(2,-1,'base')", 2))
			q := "UPDATE tr_rich SET v=$1 WHERE id=$2 RETURNING id,v,mark"
			held := triggerDDLHeld(t, p, b, c, q)
			run(fixture.Prepared(fixture.Exec(`ALTER TRIGGER old ON tr_rich RENAME TO "new ' 名"`, 0)))
			s := fixture.Select(q, []string{"id", "v", "mark"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(12), "new ' 名:kept"}}, 5, 1)
			held(s)
			run(fixture.Exec("UPDATE tr_rich SET id=id", 2))
			s = fixture.Select(q, []string{"id", "v", "mark"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(-1), "base"}}, -1, 2)
			held(s)
			run(fixture.Select("SELECT id,v,mark FROM tr_rich ORDER BY id", []string{"id", "v", "mark"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(12), "new ' 名:kept"}, {int64(2), int64(-1), "base"}}))
		})
	})
}

func TestReferenceTriggerDDLDrop(t *testing.T) {
	for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
		t.Run("identity"+mode, func(t *testing.T) {
			triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE TABLE tr_other(id integer,v integer)", "CREATE TABLE tr_empty(id integer,v integer)",
					"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "INSERT INTO tr_data VALUES($1,5) RETURNING id,v"
				held := triggerDDLHeld(t, p, b, c, q)
				run(triggerDDLError("DROP TRIGGER a ON tr_empty", "42704", `trigger "a" for table "tr_empty" does not exist`, true))
				run(fixture.Prepared(fixture.Exec("DROP TRIGGER a ON public.tr_data"+mode, 0)))
				s := triggerRows(q, []any{int64(1), int64(5)})
				s.Args = []any{1}
				held(s)
				run(triggerRows("INSERT INTO tr_other VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
				run(fixture.Exec("DROP TRIGGER IF EXISTS a ON tr_data", 0))
				run(fixture.Exec("DROP TABLE tr_other RESTRICT", 0))
				run(fixture.Exec("DROP FUNCTION tr_double() RESTRICT", 0))
				run(triggerRows("SELECT id,v FROM tr_data", []any{int64(1), int64(5)}))
			})
		})
	}
	t.Run("provider", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, name := range []string{"a", "renamed"} {
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function tr_double() because other objects depend on it")
				want.Detail = "trigger " + name + " on table tr_data depends on function tr_double()"
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				run(fixture.ErrorDiagnostic("DROP FUNCTION tr_double() RESTRICT", want))
				if name == "a" {
					run(fixture.Exec("ALTER TRIGGER a ON tr_data RENAME TO renamed", 0))
				}
			}
			run(triggerDDLError("DROP TRIGGER a ON tr_data", "42704", `trigger "a" for table "tr_data" does not exist`, false))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			userOperatorExec(t, p, b, c, "DROP FUNCTION tr_double() CASCADE", "CREATE FUNCTION tr_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=99; RETURN NEW; END$$")
			run(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(5)}))
		})
	})
	t.Run("held-dml-and-drop", triggerDDLDropHeld)
}

func triggerDDLDropHeld(t *testing.T) {
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data VALUES(2,5)", 1))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER gate BEFORE INSERT OR UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		updateQ := "UPDATE tr_data SET v=7 WHERE id=2 RETURNING id,v"
		deleteQ := "DELETE FROM tr_data WHERE id=2 RETURNING id,v"
		insertQ := "INSERT INTO tr_data VALUES(2,9) RETURNING id,v"
		update := triggerDDLHeld(t, p, b, c, updateQ)
		remove := triggerDDLHeld(t, p, b, c, deleteQ)
		insert := triggerDDLHeld(t, p, b, c, insertQ)
		update(triggerRows(updateQ))
		remove(triggerRows(deleteQ))
		insert(triggerRows(insertQ))
		dropQ := "DROP TRIGGER gate ON tr_data"
		drop := triggerDDLHeld(t, p, b, c, dropQ)
		run(fixture.Exec("ALTER TRIGGER gate ON tr_data RENAME TO renamed", 0))
		drop(triggerDDLError(dropQ, "42704", `trigger "gate" for table "tr_data" does not exist`, false))
		run(fixture.Exec("DROP TRIGGER renamed ON tr_data", 0))
		update(triggerRows(updateQ, []any{int64(2), int64(7)}))
		remove(triggerRows(deleteQ, []any{int64(2), int64(7)}))
		insert(triggerRows(insertQ, []any{int64(2), int64(9)}))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER gate BEFORE INSERT OR UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()")
		drop(fixture.Exec(dropQ, 0))
		remove(triggerRows(deleteQ, []any{int64(2), int64(9)}))
	})
}
