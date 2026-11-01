package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDeferredSavepoints(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(fixture.Exec("RELEASE s", 0))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "c"}))
		run(triggerRows("SELECT id,v FROM d", []any{int64(1), int64(10)}))
		userOperatorExec(t, p, b, c, "TRUNCATE d,audit")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(deferredCount("audit", 1))
		run(fixture.Exec("ROLLBACK TO s", 0))
		run(deferredCount("audit", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(4,40)", 1))
		run(deferredCount("audit", 0))
		run(fixture.Exec("RELEASE s", 0))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(3), nil, int64(30), int64(2), "c"}, []any{"INSERT", int64(4), nil, int64(40), int64(2), "c"}))
		userOperatorExec(t, p, b, c, "TRUNCATE d,audit")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(5,50)", 1))
		run(fixture.Exec("SAVEPOINT s", 0))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(fixture.Exec("RELEASE s", 0))
		run(deferredCount("audit", 1))
		run(fixture.Exec("COMMIT", 0))
		run(deferredCount("audit", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(6,60)", 1))
		run(deferredCount("audit", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(deferredCount("d", 1))
		run(deferredCount("audit", 1))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(7,70)", 1))
		run(deferredCount("audit", 1))
		run(fixture.Exec("COMMIT", 0))
		run(deferredCount("audit", 2))
	})
}

func TestReferenceTriggerDeferredFailures(t *testing.T) {
	t.Run("immediate-and-set", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE FUNCTION d_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,TG_NAME); IF NEW.id=2 THEN PERFORM 1/0; END IF; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY IMMEDIATE FOR EACH ROW EXECUTE FUNCTION d_fail()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(deferredFailure("INSERT INTO d(id,v) VALUES(2,20)", "d_fail"))
			run(deferredCount("d", 0))
			run(deferredCount("audit", 0))
			userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_fail()")
			run(deferredFailure("INSERT INTO d(id,v) VALUES(2,20)", "d_fail"))
			run(deferredCount("d", 0))
			run(deferredCount("audit", 0))
			userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY IMMEDIATE FOR EACH ROW EXECUTE FUNCTION d_fail()")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(deferredFailure("INSERT INTO d(id,v) VALUES(2,20)", "d_fail"))
			run(deferredAborted("SELECT 1"))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(deferredCount("d", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS c DEFERRED", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(deferredFailure("SET CONSTRAINTS c IMMEDIATE", "d_fail"))
			run(deferredAborted("SELECT 1"))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(deferredCount("audit", 0))
			run(deferredCount("d", 2))
			run(fixture.Exec("CREATE OR REPLACE FUNCTION d_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'safe'); RETURN NULL; END$$", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
			run(deferredCount("audit", 0))
			run(fixture.Exec("COMMIT", 0))
			run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(3), "safe"}, []any{"INSERT", int64(2), nil, int64(20), int64(3), "safe"}, []any{"INSERT", int64(3), nil, int64(30), int64(3), "safe"}))
		})
	})
	t.Run("terminal-data-and-catalog", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c,
				"CREATE FUNCTION d_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 7'",
				"CREATE FUNCTION d_good() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'good'); CREATE OR REPLACE FUNCTION d_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 99'; RETURN NULL; END$$",
				"CREATE FUNCTION d_bad() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'bad'); PERFORM 1/0; RETURN NULL; END$$",
				"CREATE CONSTRAINT TRIGGER a_good AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_good()",
				"CREATE CONSTRAINT TRIGGER z_bad AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_bad()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			q := "SELECT d_helper() AS v"
			h, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			checkHelper := func(n int64) {
				s := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{n}})
				run(s)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: h, SQL: q}, b.Recorder, s)
				run(fixture.Prepared(s))
			}
			checkHelper(7)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(deferredCount("audit", 0))
			checkHelper(7)
			run(deferredFailure("COMMIT", "d_bad"))
			run(deferredCount("d", 0))
			run(deferredCount("audit", 0))
			checkHelper(7)
			run(fixture.Exec("DROP TRIGGER z_bad ON d", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(deferredCount("audit", 0))
			checkHelper(7)
			run(fixture.Exec("COMMIT", 0))
			checkHelper(99)
			run(deferredAudit([]any{"INSERT", int64(2), nil, int64(20), int64(1), "good"}))
			run(triggerRows("SELECT id,v FROM d", []any{int64(2), int64(20)}))
		})
	})
}

func TestReferenceTriggerDeferredCatch(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE caught(n integer)", "CREATE TABLE outer_work(id integer)", "CREATE FUNCTION d_catch_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'attempt'); IF NEW.id=2 THEN PERFORM 1/0; END IF; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_catch_body()", "CREATE FUNCTION d_catch() RETURNS integer LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN BEGIN INSERT INTO d(id,v) VALUES(2,20); SET CONSTRAINTS c IMMEDIATE; EXCEPTION WHEN division_by_zero THEN n:=n+1; END; INSERT INTO caught VALUES(n); INSERT INTO outer_work VALUES(8); RETURN n; END$$")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO outer_work VALUES(7)", 1))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
		run(fixture.Select("SELECT d_catch() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(deferredCount("audit", 0))
		run(triggerRows("SELECT id,v FROM d", []any{int64(1), int64(10)}))
		run(fixture.Select("SELECT n FROM caught", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT id FROM outer_work ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		run(fixture.Exec("CREATE OR REPLACE FUNCTION d_catch_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'recovered'); RETURN NULL; END$$", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
		run(deferredCount("audit", 0))
		run(fixture.Exec("COMMIT", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(2), "recovered"}, []any{"INSERT", int64(3), nil, int64(30), int64(2), "recovered"}))
	})
}

func TestReferenceTriggerDeferredRecursive(t *testing.T) {
	for _, mode := range []string{"COMMIT", "SET CONSTRAINTS ALL IMMEDIATE", "SET CONSTRAINTS ca IMMEDIATE"} {
		t.Run(mode, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				deferredSetup(t, p, b, c)
				userOperatorExec(t, p, b, c, "CREATE TABLE child(id integer PRIMARY KEY)", "CREATE TABLE child_log(id integer)", "CREATE FUNCTION d_a() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'a'); INSERT INTO child VALUES(NEW.id); RETURN NULL; END$$", "CREATE FUNCTION d_b() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO child_log VALUES(NEW.id); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER ca AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_a()", "CREATE CONSTRAINT TRIGGER cb AFTER INSERT ON child DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_b()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
				run(deferredCount("audit", 0))
				run(deferredCount("child", 0))
				run(fixture.Exec(mode, 0))
				run(deferredCount("audit", 1))
				run(deferredCount("child", 1))
				if mode == "SET CONSTRAINTS ca IMMEDIATE" {
					run(deferredCount("child_log", 0))
					run(fixture.Exec("SET CONSTRAINTS cb IMMEDIATE", 0))
				}
				run(deferredCount("child_log", 1))
				if mode != "COMMIT" {
					run(fixture.Exec("COMMIT", 0))
				}
				run(deferredCount("audit", 1))
				run(deferredCount("child_log", 1))
				run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "a"}))
			})
		})
	}
	t.Run("nested-set-and-caught-tail", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TABLE child(id integer)", "CREATE TABLE child_log(id integer)", "CREATE TABLE caught(n integer)", "CREATE FUNCTION d_b() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO child_log VALUES(NEW.id); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER cb AFTER INSERT ON child DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_b()", "CREATE FUNCTION d_nested() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE n integer:=0; BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'nested'); BEGIN INSERT INTO child VALUES(NEW.id+100); SET CONSTRAINTS ALL IMMEDIATE; PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN n:=n+1; END; INSERT INTO caught VALUES(n); INSERT INTO child VALUES(NEW.id); SET CONSTRAINTS ALL IMMEDIATE; SET CONSTRAINTS ALL DEFERRED; RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER ca AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_nested()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(deferredCount("audit", 1))
			run(deferredCount("child_log", 1))
			run(fixture.Select("SELECT id FROM child_log", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(deferredCount("audit", 1))
			run(fixture.Exec("COMMIT", 0))
			run(deferredCount("audit", 2))
			run(fixture.Select("SELECT id FROM child_log ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT n FROM caught ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}}))
		})
	})
	t.Run("body-generated-foreign-and-unique", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TABLE parent(id integer PRIMARY KEY)", "CREATE TABLE child(id integer,CONSTRAINT uq UNIQUE(id) DEFERRABLE INITIALLY DEFERRED,CONSTRAINT fk FOREIGN KEY(id) REFERENCES parent(id) DEFERRABLE INITIALLY DEFERRED)", "CREATE FUNCTION d_obligations() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO child VALUES(NEW.id); INSERT INTO parent VALUES(NEW.id); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_obligations()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(fixture.Exec("COMMIT", 0))
			run(deferredCount("parent", 1))
			run(deferredCount("child", 1))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION d_obligations() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO child VALUES(NEW.id); RETURN NULL; END$$")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			d := fixture.ControlsReferenceDiagnostic("23503", `insert or update on table "child" violates foreign key constraint "fk"`)
			d.Detail = "Key (id)=(2) is not present in table \"parent\"."
			d.Schema = "public"
			d.Table = "child"
			d.Constraint = "fk"
			run(fixture.ErrorDiagnostic("COMMIT", d))
			run(deferredCount("d", 1))
			run(deferredCount("child", 1))
			userOperatorExec(t, p, b, c, "CREATE OR REPLACE FUNCTION d_obligations() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO parent VALUES(NEW.id); INSERT INTO child VALUES(NEW.id),(NEW.id); RETURN NULL; END$$")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			d = fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "uq"`)
			d.Detail = "Key (id)=(2) already exists."
			d.Schema = "public"
			d.Table = "child"
			d.Constraint = "uq"
			run(fixture.ErrorDiagnostic("COMMIT", d))
			run(deferredCount("d", 1))
			run(deferredCount("child", 1))
			run(deferredCount("parent", 1))
		})
	})
}

func TestReferenceTriggerDeferredExecutable(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
		run(fixture.Exec("UPDATE d SET v=77", 1))
		run(fixture.Exec("CREATE FUNCTION d_late() RETURNS integer LANGUAGE SQL AS 'SELECT 42'", 0))
		run(fixture.Exec("CREATE OR REPLACE FUNCTION d_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,(SELECT count(*) FROM d),'new '||d_late()::text); RETURN NULL; END$$", 0))
		run(fixture.Exec("SET CONSTRAINTS c IMMEDIATE", 0))
		run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "new 42"}))
		run(fixture.Exec("COMMIT", 0))
		run(triggerRows("SELECT id,v FROM d", []any{int64(1), int64(77)}))
	})
}
