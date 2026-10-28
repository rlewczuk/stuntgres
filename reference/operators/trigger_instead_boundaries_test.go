package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTriggerInsteadAtomicity(t *testing.T) {
	for _, failure := range []string{"row", "chain", "returning", "body", "constraint", "catalog"} {
		t.Run(failure, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				insteadSetup(t, p, b, c)
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				q := "INSERT INTO io_view VALUES(1,10),(2,20)"
				code := "22012"
				switch failure {
				case "row":
					userOperatorExec(t, p, b, c, "CREATE FUNCTION io_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.id=2 THEN PERFORM 1/0; END IF; RETURN NEW; END$$", "CREATE TRIGGER z INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_fail()")
				case "chain", "body":
					userOperatorExec(t, p, b, c, "CREATE FUNCTION io_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NEW; END$$", "CREATE TRIGGER z INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_fail()")
				case "returning":
					q += " RETURNING 1/(id-2) AS broken"
				case "constraint":
					userOperatorExec(t, p, b, c, "ALTER TABLE io_base ADD CONSTRAINT io_positive CHECK(v<20)")
					code = "23514"
				case "catalog":
					userOperatorExec(t, p, b, c, "CREATE FUNCTION io_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN CREATE TABLE io_transient(id integer); PERFORM 1/0; RETURN NEW; END$$", "CREATE TRIGGER z INSTEAD OF INSERT ON io_view FOR EACH ROW EXECUTE FUNCTION io_fail()")
				}
				check := func() {
					run(triggerRows("SELECT id,v FROM io_base"))
					run(insteadLog("SELECT op,id,ov,nv FROM io_log"))
					run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='io_transient'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				fail := fixture.Error(q, code)
				if failure == "returning" {
					fail.SelectRows = true
				}
				run(fail)
				check()
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(context.Background(), "ROLLBACK")
				run(fixture.Exec("SAVEPOINT before_failure", 0))
				run(fail)
				run(fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				run(fixture.Exec("ROLLBACK TO before_failure", 0))
				check()
				run(fixture.Exec("COMMIT", 0))
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				s := fail
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				check()
				if failure != "returning" && failure != "constraint" {
					userOperatorExec(t, p, b, c, "DROP TRIGGER z ON io_view")
				}
				run(fixture.Exec("INSERT INTO io_view VALUES(3,5)", 1))
				run(triggerRows("SELECT id,v FROM io_base", []any{int64(3), int64(6)}))
			})
		})
	}
	t.Run("caught", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			insteadSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, `CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE caught integer:=0; BEGIN BEGIN INSERT INTO io_base VALUES(NEW.id,99); INSERT INTO io_log VALUES('protected',NEW.id,NULL,99); PERFORM 1/0; EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END; INSERT INTO io_base VALUES(NEW.id,NEW.v); INSERT INTO io_log VALUES('caught',NEW.id,caught,NEW.v); RETURN NEW; END$$`)
			afterStep(t, p, b, c, prepared, triggerRows("INSERT INTO io_view VALUES(1,10) RETURNING id,v", []any{int64(1), int64(10)}))
			afterStep(t, p, b, c, prepared, triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(10)}))
			afterStep(t, p, b, c, prepared, insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"caught", int64(1), int64(1), int64(10)}))
		})
	})
}

func TestReferenceTriggerInsteadNestedAfter(t *testing.T) {
	insteadVisibility(t)
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		insteadSetupTable(t, p, b, c, "CREATE TABLE io_base(id integer PRIMARY KEY,v integer,g integer GENERATED ALWAYS AS(v*2) STORED)")
		userOperatorExec(t, p, b, c,
			"CREATE TABLE io_parent(id integer PRIMARY KEY)", "ALTER TABLE io_base ADD CONSTRAINT io_fk FOREIGN KEY(id) REFERENCES io_parent(id)",
			"CREATE TABLE io_order(seq integer GENERATED ALWAYS AS IDENTITY,kind text,id integer,v integer,g integer,n bigint)",
			"CREATE FUNCTION io_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+1; RETURN NEW; END$$",
			"CREATE FUNCTION io_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_order(kind,id,v,g,n) VALUES(TG_LEVEL,COALESCE(NEW.id,OLD.id),NEW.v,NEW.g,(SELECT count(*) FROM io_base)); RETURN NULL; END$$",
			"CREATE FUNCTION io_outer_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_order(kind,n) VALUES('outer',(SELECT count(*) FROM io_base)); RETURN NULL; END$$",
			"CREATE TRIGGER before_row BEFORE INSERT OR UPDATE ON io_base FOR EACH ROW EXECUTE FUNCTION io_before()",
			"CREATE TRIGGER after_row AFTER INSERT OR UPDATE OR DELETE ON io_base FOR EACH ROW EXECUTE FUNCTION io_after()",
			"CREATE TRIGGER after_statement AFTER INSERT OR UPDATE OR DELETE ON io_base EXECUTE FUNCTION io_after()",
			"CREATE TRIGGER after_statement AFTER INSERT OR UPDATE OR DELETE ON io_view EXECUTE FUNCTION io_outer_after()",
			`CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF TG_OP='INSERT' THEN INSERT INTO io_base(id,v) VALUES(NEW.id,NEW.v); ELSIF TG_OP='UPDATE' THEN UPDATE io_base SET v=NEW.v WHERE id=OLD.id; ELSE DELETE FROM io_base WHERE id=OLD.id; END IF; INSERT INTO io_order(kind,id,n) VALUES('resume',COALESCE(NEW.id,OLD.id),(SELECT count(*) FROM io_base)); RETURN COALESCE(NEW,OLD); END$$`)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO io_parent VALUES(1),(2)", 2))
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("INSERT INTO io_view VALUES(1,10),(2,20)", 2))
		run(fixture.Select("SELECT id,v,g FROM io_base ORDER BY id", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(22)}, {int64(2), int64(21), int64(42)}}))
		// Order within each nested command is strict; candidate order is not assumed.
		run(fixture.Select("SELECT kind,id,v,g,n FROM io_order ORDER BY seq", []string{"kind", "id", "v", "g", "n"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT8"}, [][]any{{"ROW", int64(1), int64(11), int64(22), int64(1)}, {"STATEMENT", nil, nil, nil, int64(1)}, {"resume", int64(1), nil, nil, int64(1)}, {"ROW", int64(2), int64(21), int64(42), int64(2)}, {"STATEMENT", nil, nil, nil, int64(2)}, {"resume", int64(2), nil, nil, int64(2)}, {"outer", nil, nil, nil, int64(2)}}))
		run(fixture.Exec("DELETE FROM io_order", 7))
		run(fixture.Exec("UPDATE io_view SET v=30 WHERE id=1", 1))
		run(fixture.Exec("DELETE FROM io_view WHERE id=2", 1))
		run(fixture.Select("SELECT id,v,g FROM io_base", []string{"id", "v", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(31), int64(62)}}))
		run(fixture.Exec("DELETE FROM io_order", 8))
		run(fixture.Exec("UPDATE io_view SET v=0 WHERE false", 0))
		run(fixture.Select("SELECT kind,n FROM io_order", []string{"kind", "n"}, []string{"TEXT", "INT8"}, [][]any{{"outer", int64(1)}}))
		userOperatorExec(t, p, b, c, "CREATE FUNCTION io_after_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$", "CREATE TRIGGER z AFTER INSERT ON io_base FOR EACH ROW EXECUTE FUNCTION io_after_fail()")
		run(fixture.Error("INSERT INTO io_view VALUES(2,20)", "22012"))
		run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(31)}))
		run(fixture.Select("SELECT kind,n FROM io_order", []string{"kind", "n"}, []string{"TEXT", "INT8"}, [][]any{{"outer", int64(1)}}))
		userOperatorExec(t, p, b, c, `CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$DECLARE caught integer:=0; BEGIN BEGIN INSERT INTO io_base(id,v) VALUES(NEW.id,NEW.v); EXCEPTION WHEN division_by_zero THEN caught:=caught+1; END; INSERT INTO io_log VALUES('caught',NEW.id,caught,NEW.v); RETURN NEW; END$$`)
		run(fixture.Exec("INSERT INTO io_view VALUES(2,20)", 1))
		run(insteadLog("SELECT op,id,ov,nv FROM io_log", []any{"caught", int64(2), int64(1), int64(20)}))
		run(triggerRows("SELECT id,v FROM io_base", []any{int64(1), int64(31)}))
		userOperatorExec(t, p, b, c, "DROP TRIGGER z ON io_base", "CREATE VIEW io_nested AS SELECT id,v FROM io_view", "CREATE OR REPLACE FUNCTION io_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_base(id,v) VALUES(NEW.id,NEW.v); RETURN NEW; END$$", "CREATE FUNCTION io_nested_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO io_view VALUES(NEW.id,NEW.v); RETURN NEW; END$$", "CREATE TRIGGER body INSTEAD OF INSERT ON io_nested FOR EACH ROW EXECUTE FUNCTION io_nested_body()")
		run(triggerRows("INSERT INTO io_nested VALUES(2,40) RETURNING id,v", []any{int64(2), int64(40)}))
		run(triggerRows("SELECT id,v FROM io_base ORDER BY id", []any{int64(1), int64(31)}, []any{int64(2), int64(41)}))
	})
}
