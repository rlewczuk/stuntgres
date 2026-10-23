package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreReturns(t *testing.T) {
	for _, tc := range []struct{ name, body, state, message string }{
		{"null", "BEGIN RETURN NULL; END", "", ""},
		{"compatible", "BEGIN RETURN ROW(NEW.id,NEW.v); END", "", ""},
		{"scalar", "BEGIN RETURN 7; END", "42804", "cannot return non-composite value from function returning composite type"},
		{"width", "BEGIN RETURN ROW(NEW.id); END", "42804", "returned row structure does not match the structure of the triggering table"},
		{"type", "BEGIN RETURN ROW(NEW.id,'bad'::text); END", "42804", "returned row structure does not match the structure of the triggering table"},
		{"fallthrough", "BEGIN END", "2F005", "control reached end of trigger procedure without RETURN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE tr_return(id integer,v integer)", "CREATE FUNCTION tr_return_fn() RETURNS trigger LANGUAGE plpgsql AS $$"+tc.body+"$$", "CREATE TRIGGER a BEFORE INSERT ON tr_return FOR EACH ROW EXECUTE FUNCTION tr_return_fn()")
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				q := "INSERT INTO tr_return VALUES(1,5)"
				if tc.state != "" {
					d := fixture.ControlsReferenceDiagnostic(tc.state, tc.message)
					d.Context = "PL/pgSQL function tr_return_fn()"
					switch tc.name {
					case "scalar":
						d.Context += " line 1 at RETURN"
					case "width":
						d.Context += " during function exit"
						d.Detail = "Number of returned columns (1) does not match expected column count (2)."
					case "type":
						d.Context += " during function exit"
						d.Detail = "Returned type text does not match expected type integer in column \"v\" (position 2)."
					}
					run(fixture.ErrorDiagnostic(q, d))
				} else if tc.name == "null" {
					run(fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 1))
				}
				var rows [][]any
				if tc.name == "compatible" {
					rows = [][]any{{int64(1), int64(5)}}
				}
				run(triggerRows("SELECT id,v FROM tr_return ORDER BY id", rows...))
				// Direct invocation must not become a successful scalar NULL call.
				d := fixture.ControlsReferenceDiagnostic("0A000", "trigger functions can only be called as triggers")
				d.Context = "compilation of PL/pgSQL function \"tr_return_fn\" near line 1"
				run(fixture.ErrorDiagnostic("SELECT tr_return_fn()", d))
			})
		})
	}
	t.Run("declared_argument", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := fixture.ControlsReferenceDiagnostic("42P13", "trigger functions cannot have declared arguments")
			d.Hint = "The arguments of the trigger can be accessed through TG_NARGS and TG_ARGV instead."
			d.Context = "compilation of PL/pgSQL function \"tr_bad_arg\" near line 1"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("CREATE FUNCTION tr_bad_arg(x integer) RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", d))
		})
	})
}

func TestReferenceTriggerCoreAtomicity(t *testing.T) {
	for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
		t.Run(op, func(t *testing.T) {
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				triggerSetup(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("INSERT INTO tr_data VALUES(1,10),(2,20),(3,30)", 3))
				body := "BEGIN NEW.v := 100/(NEW.id-2); RETURN NEW; END"
				if op == "DELETE" {
					body = "BEGIN OLD.v := 100/(OLD.id-2); RETURN OLD; END"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_error() RETURNS trigger LANGUAGE plpgsql AS $$"+body+"$$", "CREATE TRIGGER a BEFORE "+op+" ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_error()")
				q := "UPDATE tr_data SET v=5"
				if op == "INSERT" {
					q = "INSERT INTO tr_data VALUES(4,5),(2,5),(5,5)"
				}
				if op == "DELETE" {
					q = "DELETE FROM tr_data"
				}
				run(fixture.Error(q, "22012"))
				run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
				run(fixture.Exec("CREATE TABLE tr_private(x integer)", 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO tr_private VALUES(99)", 1))
				run(fixture.Error(q, "22012"))
				run(fixture.Error("SELECT 1", "25P02"))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Select("SELECT count(*) AS v FROM tr_private", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
			})
		})
	}
}

func TestReferenceTriggerCoreOrdering(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tr_order(id integer PRIMARY KEY,v integer NOT NULL CHECK(v>0),g integer GENERATED ALWAYS AS(v*2) STORED)",
			"CREATE FUNCTION tr_repair() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := COALESCE(NEW.v,4)+1; RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tr_order FOR EACH ROW EXECUTE FUNCTION tr_repair()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Select("INSERT INTO tr_order(id,v) VALUES(1,NULL),(2,0) RETURNING id,v,g", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(5), int64(10)}, {int64(2), int64(1), int64(2)}}))
		run(fixture.Error("INSERT INTO tr_order(id,v) VALUES(3,2),(4,-2)", "23514"))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_collide() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.id := 1; RETURN NEW; END$$", "CREATE TRIGGER z BEFORE INSERT ON tr_order FOR EACH ROW EXECUTE FUNCTION tr_collide()")
		run(fixture.Error("INSERT INTO tr_order(id,v) VALUES(5,2)", "23505"))
		run(triggerRows("SELECT id,v FROM tr_order ORDER BY id", []any{int64(1), int64(5)}, []any{int64(2), int64(1)}))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_make_null() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NULL; RETURN NEW; END$$", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_order FOR EACH ROW EXECUTE FUNCTION tr_make_null()")
		run(fixture.Error("INSERT INTO tr_order(id,v) VALUES(6,2)", "23502"))
		run(triggerRows("SELECT id,v FROM tr_order ORDER BY id", []any{int64(1), int64(5)}, []any{int64(2), int64(1)}))
	})
}

func TestReferenceTriggerCorePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tr_actor"})
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE ROLE tr_actor", "GRANT USAGE ON SCHEMA public TO tr_actor", "GRANT SELECT,INSERT,UPDATE ON tr_data TO tr_actor", "REVOKE EXECUTE ON FUNCTION tr_double() FROM PUBLIC", "SET SESSION AUTHORIZATION tr_actor")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()"
		run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table tr_data")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT TRIGGER ON tr_data TO tr_actor", "SET SESSION AUTHORIZATION tr_actor")
		run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function tr_double")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION tr_double() TO tr_actor", "SET SESSION AUTHORIZATION tr_actor", q, "RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION tr_double() FROM tr_actor", "SET SESSION AUTHORIZATION tr_actor")
		run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
		run(fixture.ErrorDiagnostic("CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function tr_double")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE INSERT ON tr_data FROM tr_actor", "SET SESSION AUTHORIZATION tr_actor")
		run(fixture.ErrorDiagnostic("INSERT INTO tr_data VALUES(2,5)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table tr_data")))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT INSERT ON tr_data TO tr_actor", "CREATE FUNCTION tr_body_helper(v integer) RETURNS integer LANGUAGE SQL AS 'SELECT v+1'", "CREATE FUNCTION tr_body_wrapper() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := tr_body_helper(NEW.v); RETURN NEW; END$$", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_body_wrapper()", "REVOKE EXECUTE ON FUNCTION tr_body_helper(integer) FROM PUBLIC", "SET SESSION AUTHORIZATION tr_actor")
		run(fixture.ErrorDiagnostic("SELECT tr_body_helper(5)", fixture.ControlsReferenceDiagnostic("42501", "permission denied for function tr_body_helper")))
		run(fixture.Error("INSERT INTO tr_data VALUES(2,5)", "42501"))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION tr_body_helper(integer) TO tr_actor", "SET SESSION AUTHORIZATION tr_actor")
		run(triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), int64(6)}))
	})
}

func TestReferenceTriggerCoreVisibility(t *testing.T) {
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION tr_visible() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := (SELECT count(*)::integer FROM tr_data); RETURN NEW; END$$",
			"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_gate()",
			"CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_visible()")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO tr_data VALUES(1,99),(2,99),(3,99),(4,99)", 3))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM tr_data ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}))
	})
}
