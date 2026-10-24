package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerAtomicity(t *testing.T) {
	t.Run("private-events", transactionalTriggerPrivateEvents)
	for _, held := range []bool{false, true} {
		name := "direct"
		if held {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerSetup(t, p, b, c)
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION tr_error() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=100/(NEW.id-2); RETURN NEW; END$$",
					"CREATE FUNCTION tr_wrong() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				defer c.ExecContext(context.Background(), "ROLLBACK")
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE TRIGGER a BEFORE INSERT OR UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()", 0))
				run(fixture.Exec("CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_error()", 0))
				run(fixture.Exec("INSERT INTO tr_data VALUES(1,5),(2,5),(3,5)", 2))
				run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(-100)}, []any{int64(3), int64(100)}))
				q := "INSERT INTO tr_data VALUES(2,5) RETURNING id,v"
				if held {
					triggerDDLHeld(t, p, b, c, q)(triggerRows(q))
				} else {
					run(triggerRows(q))
				}
				run(fixture.Exec("SAVEPOINT keep", 0))
				run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
				q = "INSERT INTO tr_data VALUES(4,5),(2,5),(5,5)"
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				d.Context = "PL/pgSQL function tr_error() line 1 at assignment"
				s := fixture.ErrorDiagnostic(q, d)
				s.ErrorStage = "execute"
				if held {
					triggerDDLHeld(t, p, b, c, q)(s)
				} else {
					run(s)
				}
				run(triggerDDLError("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
				run(fixture.Exec("ROLLBACK TO keep", 0))
				run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(-100)}, []any{int64(3), int64(100)}))
				run(fixture.Exec("INSERT INTO tr_data VALUES(2,5)", 0))
				for _, tc := range []struct{ fn, code, message string }{
					{"tr_missing", "42883", "function tr_missing() does not exist"},
					{"tr_wrong", "42P17", "function tr_wrong must return type trigger"},
				} {
					q = "CREATE OR REPLACE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION " + tc.fn + "()"
					s = triggerDDLError(q, tc.code, tc.message, false)
					if held {
						triggerDDLHeld(t, p, b, c, q)(s)
					} else {
						run(s)
					}
					run(fixture.Exec("ROLLBACK TO keep", 0))
					run(fixture.Exec("INSERT INTO tr_data VALUES(2,5)", 0))
				}
				run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
				run(fixture.Exec("CREATE OR REPLACE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW WHEN(10/(NEW.id-2)>0) EXECUTE FUNCTION tr_double()", 0))
				run(fixture.ErrorDiagnostic("INSERT INTO tr_data VALUES(4,5),(2,5),(5,5)", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
				run(fixture.Exec("ROLLBACK TO keep", 0))
				run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(-100)}, []any{int64(3), int64(100)}))
				run(fixture.Exec("RELEASE keep", 0))
				run(fixture.Exec("COMMIT", 0))
				run(triggerRows("INSERT INTO tr_data VALUES(4,5) RETURNING id,v", []any{int64(4), int64(50)}))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("CREATE TABLE tr_private(id integer,v integer)", 0))
				run(fixture.Exec("CREATE TRIGGER a BEFORE INSERT ON tr_private FOR EACH ROW EXECUTE FUNCTION tr_add(7)", 0))
				run(triggerRows("INSERT INTO tr_private VALUES(1,5) RETURNING id,v", []any{int64(1), int64(12)}))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='tr_private' AND relnamespace='public'::regnamespace", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}

func transactionalTriggerPrivateEvents(t *testing.T) {
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=CASE WHEN TG_OP='UPDATE' AND TG_NARGS=1 AND TG_ARGV[1] IS NULL THEN OLD.v+NEW.v+TG_ARGV[0]::integer ELSE -999 END; RETURN NEW; END$$",
			"CREATE FUNCTION tr_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v:=999; RETURN OLD; END$$",
			"CREATE FUNCTION tr_delete_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v:=1/0; RETURN OLD; END$$")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("INSERT INTO tr_data VALUES(1,10),(2,20),(3,30)", 3))
		q := "UPDATE tr_data SET v=5 RETURNING id,v"
		held := triggerDDLHeld(t, p, b, c, q)
		defer c.ExecContext(context.Background(), "ROLLBACK")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE TRIGGER a BEFORE UPDATE OF v ON tr_data FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION tr_update('3')", 0))
		run(fixture.Exec("CREATE TRIGGER b BEFORE UPDATE OR DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()", 0))
		run(fixture.Exec("SAVEPOINT events", 0))
		run(fixture.Exec("UPDATE tr_data SET id=id", 2))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
		s := triggerRows(q, []any{int64(1), int64(18)}, []any{int64(3), int64(38)})
		s.Bag = true
		held(s)
		run(triggerRows("UPDATE tr_data SET v=v WHERE id=1 RETURNING id,v", []any{int64(1), int64(39)}))
		run(fixture.Exec("CREATE OR REPLACE TRIGGER a BEFORE DELETE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_delete()", 0))
		run(fixture.Exec("CREATE TRIGGER z BEFORE DELETE ON tr_data FOR EACH ROW WHEN(OLD.v=999) EXECUTE FUNCTION tr_delete_fail()", 0))
		s = triggerRows("DELETE FROM tr_data RETURNING id,v", []any{int64(1), int64(39)}, []any{int64(3), int64(38)})
		s.Bag = true
		run(s)
		run(triggerRows("SELECT id,v FROM tr_data", []any{int64(2), int64(20)}))
		run(fixture.Exec("ROLLBACK TO events", 0))
		s = triggerRows(q, []any{int64(1), int64(18)}, []any{int64(3), int64(38)})
		s.Bag = true
		held(s)
		run(fixture.Exec("RELEASE events", 0))
		run(fixture.Exec("ROLLBACK", 0))
		s = triggerRows(q, []any{int64(1), int64(5)}, []any{int64(2), int64(5)}, []any{int64(3), int64(5)})
		s.Bag = true
		held(s)
	})
}
