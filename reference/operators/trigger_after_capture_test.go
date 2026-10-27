package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func afterCaptureSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE cap_parent(id integer)",
		"CREATE TABLE cap_child(id integer)",
		"CREATE TABLE cap_bodies(name text,id integer)",
		"CREATE TABLE cap_helpers(name text,children bigint,bodies bigint)",
		"CREATE TABLE cap_resume(value integer,children bigint,bodies bigint,helpers bigint,caught integer)",
		"CREATE FUNCTION cap_condition(label text) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO cap_helpers VALUES(label,(SELECT count(*) FROM cap_child),(SELECT count(*) FROM cap_bodies)); IF label='f' THEN RETURN false; END IF; RETURN NULL; END$$",
		"CREATE FUNCTION cap_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO cap_bodies VALUES(TG_NAME,NEW.id); IF NEW.id<0 THEN PERFORM 1/0; END IF; RETURN NULL; END$$",
		"CREATE TRIGGER r AFTER INSERT ON cap_child FOR EACH ROW EXECUTE FUNCTION cap_body()",
		"CREATE TRIGGER a AFTER INSERT ON cap_child EXECUTE FUNCTION cap_body()",
		"CREATE TRIGGER f AFTER INSERT ON cap_child WHEN(cap_condition('f')) EXECUTE FUNCTION cap_body()",
		"CREATE TRIGGER n AFTER INSERT ON cap_child WHEN(cap_condition('n')) EXECUTE FUNCTION cap_body()")
}

func TestReferenceTriggerAfterCaptureCTE(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		afterCaptureSetup(t, p, b, c)
		userOperatorExec(t, p, b, c,
			"CREATE FUNCTION cap_outer() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE v integer; BEGIN WITH x AS (INSERT INTO cap_child VALUES(NEW.id) RETURNING id) SELECT id INTO v FROM x; INSERT INTO cap_resume VALUES(v,(SELECT count(*) FROM cap_child),(SELECT count(*) FROM cap_bodies),(SELECT count(*) FROM cap_helpers),0); WITH x AS (INSERT INTO cap_child VALUES(NEW.id+10) RETURNING id) SELECT 7 INTO v; INSERT INTO cap_resume VALUES(v,(SELECT count(*) FROM cap_child),(SELECT count(*) FROM cap_bodies),(SELECT count(*) FROM cap_helpers),0); RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER INSERT ON cap_parent FOR EACH ROW EXECUTE FUNCTION cap_outer()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Select("INSERT INTO cap_parent VALUES(1) RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT value,children,bodies,helpers,caught FROM cap_resume ORDER BY children", []string{"value","children","bodies","helpers","caught"}, []string{"INT4","INT8","INT8","INT8","INT4"}, [][]any{{int64(1),int64(1),int64(2),int64(2),int64(0)},{int64(7),int64(2),int64(4),int64(4),int64(0)}}))
		run(fixture.Select("SELECT id FROM cap_child ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)},{int64(11)}}))
		s := fixture.Select("SELECT name,id FROM cap_bodies", []string{"name","id"}, []string{"TEXT","INT4"}, [][]any{{"r",int64(1)},{"a",nil},{"r",int64(11)},{"a",nil}})
		s.Bag = true
		run(s)
		run(fixture.Select("SELECT name,children,bodies FROM cap_helpers ORDER BY children,name", []string{"name","children","bodies"}, []string{"TEXT","INT8","INT8"}, [][]any{{"f",int64(1),int64(0)},{"n",int64(1),int64(0)},{"f",int64(2),int64(2)},{"n",int64(2),int64(2)}}))
	})
}

func TestReferenceTriggerAfterCaptureCTEFailure(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		name := "unreferenced"
		if consumed { name = "consumed" }
		for _, phase := range []string{"body", "qualification"} {
		t.Run(name+"/"+phase, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				afterCaptureSetup(t, p, b, c)
				failingFunction := "cap_body"
				if phase == "qualification" {
					failingFunction = "cap_fail_condition"
					userOperatorExec(t, p, b, c,
						"CREATE FUNCTION cap_fail_condition() RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN IF (SELECT count(*) FROM cap_child WHERE id<0)>0 THEN INSERT INTO cap_helpers VALUES('z',99,99); PERFORM 1/0; END IF; RETURN false; END$$",
						"CREATE TRIGGER z AFTER INSERT ON cap_child WHEN(cap_fail_condition()) EXECUTE FUNCTION cap_body()")
				}
				capture := "WITH x AS (INSERT INTO cap_child VALUES(-1) RETURNING id) SELECT 7 INTO v"
				if consumed { capture = "WITH x AS (INSERT INTO cap_child VALUES(-1) RETURNING id) SELECT id INTO v FROM x" }
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION cap_outer() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE v integer; BEGIN "+capture+"; RETURN NULL; END$$",
					"CREATE TRIGGER r AFTER INSERT ON cap_parent FOR EACH ROW EXECUTE FUNCTION cap_outer()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				queryContext := strings.TrimRight(strings.Replace(capture, "INTO v", "      ", 1), " ")
				d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function "+failingFunction+"() line 1 at PERFORM\nSQL statement \""+queryContext+"\"\nPL/pgSQL function cap_outer() line 1 at SQL statement"
				failure := fixture.ErrorDiagnostic("INSERT INTO cap_parent VALUES(1) RETURNING id", d)
				failure.SelectRows = true
				run(failure)
				for _, table := range []string{"cap_parent","cap_child","cap_bodies","cap_helpers","cap_resume"} {
					run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				userOperatorExec(t, p, b, c,
					"CREATE OR REPLACE FUNCTION cap_outer() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE v integer; caught integer:=0; BEGIN BEGIN "+capture+"; EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END; WITH x AS (INSERT INTO cap_child VALUES(NEW.id) RETURNING id) SELECT id INTO v FROM x; INSERT INTO cap_resume VALUES(v,(SELECT count(*) FROM cap_child),(SELECT count(*) FROM cap_bodies),(SELECT count(*) FROM cap_helpers),caught); RETURN NULL; END$$")
				run(fixture.Exec("INSERT INTO cap_parent VALUES(2)", 1))
				run(fixture.Select("SELECT value,children,bodies,helpers,caught FROM cap_resume", []string{"value","children","bodies","helpers","caught"}, []string{"INT4","INT8","INT8","INT8","INT4"}, [][]any{{int64(2),int64(1),int64(2),int64(2),int64(1)}}))
				run(fixture.Select("SELECT id FROM cap_child", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s := fixture.Select("SELECT name,id FROM cap_bodies", []string{"name","id"}, []string{"TEXT","INT4"}, [][]any{{"r",int64(2)},{"a",nil}})
				s.Bag = true
				run(s)
				run(fixture.Select("SELECT name,children,bodies FROM cap_helpers ORDER BY name", []string{"name","children","bodies"}, []string{"TEXT","INT8","INT8"}, [][]any{{"f",int64(1),int64(0)},{"n",int64(1),int64(0)}}))
			})
		})
		}
	}
}

func TestReferenceTriggerAfterCaptureCTECancellation(t *testing.T) {
	for _, consumed := range []bool{false, true} {
		name := "unreferenced"
		if consumed { name = "consumed" }
		t.Run(name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				afterCaptureSetup(t, p, b, c)
				capture := "WITH x AS (INSERT INTO cap_child VALUES(NEW.id) RETURNING id) SELECT 7 INTO v"
				if consumed { capture = "WITH x AS (INSERT INTO cap_child VALUES(NEW.id) RETURNING id) SELECT id INTO v FROM x" }
				userOperatorExec(t, p, b, c,
					"CREATE FUNCTION cap_outer() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE v integer; BEGIN "+capture+"; INSERT INTO cap_resume VALUES(v,0,0,0,0); RETURN NULL; END$$",
					"CREATE TRIGGER r AFTER INSERT ON cap_parent FOR EACH ROW EXECUTE FUNCTION cap_outer()")
				q := "INSERT INTO cap_parent VALUES(9) RETURNING id"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(p.Ctx, q)
					if err != nil { t.Fatal(err) }
					defer stmt.Close()
				}
				queryContext := strings.TrimRight(strings.Replace(capture, "INTO v", "      ", 1), " ")
				// Hold the exact WHEN helper's write target. Top-level "active"
				// admission alone cannot distinguish qualification from body firing.
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer holder.Close()
				fixture.LockExec(t, p.Ctx, holder, "BEGIN", 0)
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE cap_helpers IN ACCESS EXCLUSIVE MODE", 0)
				waiterPID, holderPID, oid := 0, 0, int64(0)
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
					oid = fixture.LockOID(t, p.Ctx, "cap_helpers")
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				finished := make(chan struct{})
				join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(finished)
					var rows *sql.Rows
					var err error
					if stmt != nil { rows, err = stmt.QueryContext(ctx) } else { rows, err = c.QueryContext(ctx, q) }
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				joined := false
				defer func() {
					cancel()
					if !joined {
						outcome := join()
						if outcome.Rows != nil { outcome.Rows.Close() }
					}
				}()
				fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "RowExclusiveLock", finished)
				if b.PG {
					var signaled bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", waiterPID).Scan(&signaled); err != nil || !signaled {
						t.Fatal("canceling admitted capture", signaled, err)
					}
				} else {
					cancel()
				}
				outcome := join()
				joined = true
				if outcome.Rows != nil { outcome.Rows.Close(); t.Fatal("canceled capture exposed rows") }
				if outcome.Result != nil { t.Fatal("canceled capture exposed result") }
				if b.PG {
					d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
					d.Context = "SQL statement \"INSERT INTO cap_helpers VALUES(label,(SELECT count(*) FROM cap_child),(SELECT count(*) FROM cap_bodies))\"\nPL/pgSQL function cap_condition(text) line 1 at SQL statement\nSQL statement \""+queryContext+"\"\nPL/pgSQL function cap_outer() line 1 at SQL statement"
					fixture.AssertDiagnostic(t, outcome.Err, &d)
				} else {
					fixture.LexicalOrdinary(t, outcome.Err, context.Canceled)
				}
				fixture.LockExec(t, p.Ctx, holder, "ROLLBACK", 0)
				// A pq context interruption may retire its borrower. Inspect and
				// execute the next command on a fresh connection in both modes.
				fresh, err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer fresh.Close()
				run := func(s fixture.Step) { afterStep(t, p, b, fresh, prepared, s) }
				for _, table := range []string{"cap_parent","cap_child","cap_bodies","cap_helpers","cap_resume"} {
					run(fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				run(fixture.Exec("INSERT INTO cap_parent VALUES(2)", 1))
				s := fixture.Select("SELECT name,id FROM cap_bodies", []string{"name","id"}, []string{"TEXT","INT4"}, [][]any{{"r",int64(2)},{"a",nil}})
				s.Bag = true
				run(s)
				run(fixture.Select("SELECT name,children,bodies FROM cap_helpers ORDER BY name", []string{"name","children","bodies"}, []string{"TEXT","INT8","INT8"}, [][]any{{"f",int64(1),int64(0)},{"n",int64(1),int64(0)}}))
				run(fixture.Select("SELECT count(*) AS n FROM cap_resume", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
}
