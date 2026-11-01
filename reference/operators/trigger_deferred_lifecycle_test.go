package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDeferredLookup(t *testing.T) {
	t.Run("first-schema-and-atomic-names", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			fixture.SchemaReferenceCleanup(t, "da", "db")
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE SCHEMA da", "CREATE SCHEMA db", "CREATE TABLE da.t1(id integer,v integer)", "CREATE TABLE da.t2(id integer,v integer)", "CREATE TABLE db.t3(id integer,v integer)", "CREATE FUNCTION d_named() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,TG_TABLE_SCHEMA||'.'||TG_TABLE_NAME); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON da.t1 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_named()", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON da.t2 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_named()", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON db.t3 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_named()", "SET search_path=da,db,public")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO da.t1 VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO da.t2 VALUES(2,20)", 1))
			run(fixture.Exec("INSERT INTO db.t3 VALUES(3,30)", 1))
			run(deferredCount("audit", 0))
			run(fixture.Exec("SET CONSTRAINTS c,c IMMEDIATE", 0))
			run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(0), "da.t1"}, []any{"INSERT", int64(2), nil, int64(20), int64(0), "da.t2"}))
			run(fixture.Exec("SET CONSTRAINTS db.c IMMEDIATE", 0))
			run(deferredCount("audit", 3))
			run(fixture.Exec("ROLLBACK", 0))
			userOperatorExec(t, p, b, c, "CREATE TRIGGER ordinary AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()", "CREATE TABLE da.t4(id integer,v integer)", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON da.t4 NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION d_named()", "CREATE CONSTRAINT TRIGGER \"Quoted C\" AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_log()")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(deferredMissing("SET CONSTRAINTS ordinary IMMEDIATE", "ordinary"))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.ErrorDiagnostic("SET CONSTRAINTS c DEFERRED", fixture.ControlsReferenceDiagnostic("42809", `constraint "c" is not deferrable`)))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("INSERT INTO da.t1 VALUES(4,40)", 1))
			run(deferredCount("audit", 1))
			run(fixture.Exec("SET CONSTRAINTS \"Quoted C\" DEFERRED", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(5,50)", 1))
			run(deferredCount("audit", 2))
			run(fixture.Exec("SET CONSTRAINTS \"Quoted C\",\"Quoted C\" IMMEDIATE", 0))
			run(deferredCount("audit", 3))
			run(fixture.Exec("ROLLBACK", 0))
		})
	})
	t.Run("individual-partition-names", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			fixture.SchemaReferenceCleanup(t, "da", "db")
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE SCHEMA da", "CREATE SCHEMA db", "CREATE TABLE da.root(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE db.older PARTITION OF da.root FOR VALUES FROM(0) TO(100)", "CREATE TABLE da.local PARTITION OF da.root FOR VALUES FROM(100) TO(200)", "CREATE FUNCTION d_partition() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,TG_TABLE_NAME||':'||TG_NAME); RETURN NULL; END$$", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON da.root DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_partition()", "ALTER TRIGGER c ON da.root RENAME TO renamed", "ALTER TABLE da.root RENAME CONSTRAINT c TO root_c", "CREATE TABLE db.later(id integer,v integer)", "ALTER TABLE da.root ATTACH PARTITION db.later FOR VALUES FROM(200) TO(300)")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, deferredOIDCarriers(b, s)) }
			run(fixture.Select("SELECT r.relname,t.tgname,c.conname,c.conparentid,c.conislocal,c.coninhcount,c.connoinherit FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint JOIN pg_class r ON r.oid=t.tgrelid WHERE r.relname IN('root','older','local','later') ORDER BY r.relname", []string{"relname", "tgname", "conname", "conparentid", "conislocal", "coninhcount", "connoinherit"}, []string{"NAME", "NAME", "NAME", "OID", "BOOL", "INT2", "BOOL"}, [][]any{{"later", "renamed", "renamed", "0", true, int64(0), true}, {"local", "renamed", "c", "0", true, int64(0), true}, {"older", "renamed", "c", "0", true, int64(0), true}, {"root", "renamed", "root_c", "0", true, int64(0), true}}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO da.root VALUES(1,10),(101,20),(201,30)", 3))
			run(deferredCount("audit", 0))
			run(fixture.Exec("SET CONSTRAINTS da.root_c IMMEDIATE", 0))
			run(deferredCount("audit", 0))
			run(fixture.Exec("SET CONSTRAINTS da.c IMMEDIATE", 0))
			run(deferredAudit([]any{"INSERT", int64(101), nil, int64(20), int64(0), "local:renamed"}))
			run(fixture.Exec("SET CONSTRAINTS db.c IMMEDIATE", 0))
			run(deferredCount("audit", 2))
			run(fixture.Exec("SET CONSTRAINTS db.renamed IMMEDIATE", 0))
			run(deferredCount("audit", 3))
			run(fixture.Exec("COMMIT", 0))
			run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(0), "older:renamed"}, []any{"INSERT", int64(101), nil, int64(20), int64(0), "local:renamed"}, []any{"INSERT", int64(201), nil, int64(30), int64(0), "later:renamed"}))
		})
	})
}

func TestReferenceTriggerDeferredDDL(t *testing.T) {
	t.Run("pending-commands", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"), "CREATE INDEX d_v ON d(v)")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			for _, tc := range []struct{ q, command string }{
				{"ALTER TABLE d ADD COLUMN extra integer", "ALTER TABLE"}, {"ALTER TABLE d DISABLE TRIGGER c", "ALTER TABLE"}, {"ALTER TABLE d ENABLE TRIGGER c", "ALTER TABLE"}, {"ALTER TABLE d DROP CONSTRAINT c", "ALTER TABLE"}, {"DROP TABLE d", "DROP TABLE"}, {"TRUNCATE d RESTART IDENTITY", "TRUNCATE"}, {"CLUSTER d USING d_v", "CLUSTER"},
			} {
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.ErrorDiagnostic(tc.q, fixture.ControlsReferenceDiagnostic("55006", "cannot "+tc.command+" \"d\" because it has pending trigger events")))
				run(deferredAborted("SELECT 1"))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.Exec("RELEASE s", 0))
				run(deferredCount("audit", 0))
				run(triggerRows("SELECT id,v FROM d", []any{int64(1), int64(10)}))
			}
			run(fixture.Exec("ALTER TRIGGER c ON d RENAME TO renamed", 0))
			run(fixture.Exec("ALTER TABLE d RENAME CONSTRAINT c TO selected", 0))
			run(fixture.Exec("SAVEPOINT s", 0))
			run(deferredMissing("SET CONSTRAINTS c IMMEDIATE", "c"))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("SET CONSTRAINTS selected IMMEDIATE", 0))
			run(deferredAudit([]any{"INSERT", int64(1), nil, int64(10), int64(1), "renamed"}))
			run(fixture.Exec("COMMIT", 0))
			for _, suffix := range []string{"", " CASCADE"} {
				d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop constraint selected on table d because trigger renamed on table d requires it")
				d.Hint = "You can drop trigger renamed on table d instead."
				run(fixture.ErrorDiagnostic("ALTER TABLE d DROP CONSTRAINT selected"+suffix, d))
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(fixture.Exec("DROP TRIGGER renamed ON d", 0))
			for _, command := range []string{"DROP TABLE", "TRUNCATE"} {
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.ErrorDiagnostic(command+" d", fixture.ControlsReferenceDiagnostic("55006", "cannot "+command+" \"d\" because it has pending trigger events")))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.Exec("RELEASE s", 0))
			}
			run(fixture.Exec("SAVEPOINT s", 0))
			run(deferredMissing("SET CONSTRAINTS selected IMMEDIATE", "selected"))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(deferredCount("audit", 1))
			run(fixture.Exec("TRUNCATE d", 0))
			run(fixture.Exec("DROP TABLE d", 0))
			run(fixture.Exec("COMMIT", 0))
			run(deferredCount("audit", 1))
		})
	})
	t.Run("drop-recreate-and-from", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
			run(fixture.Exec("DROP TRIGGER c ON d", 0))
			run(fixture.Exec(deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"), 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(deferredAudit([]any{"INSERT", int64(2), nil, int64(20), int64(2), "c"}))
			run(fixture.Exec("COMMIT", 0))
			userOperatorExec(t, p, b, c, "DROP TRIGGER c ON d", "CREATE TABLE referenced(id integer)", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d FROM referenced DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_log()")
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(3,30)", 1))
			run(fixture.Exec("DROP TABLE referenced", 0))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(fixture.Exec("COMMIT", 0))
			run(deferredCount("d", 3))
			run(deferredCount("audit", 1))
			run(fixture.Select("SELECT count(*) AS n FROM pg_trigger WHERE tgrelid='d'::regclass", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			userOperatorExec(t, p, b, c, deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(4,40)", 1))
			run(fixture.Exec("SAVEPOINT s", 0))
			d := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop function d_log() because other objects depend on it")
			d.Detail = "trigger c on table d depends on function d_log()"
			d.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP FUNCTION d_log()", d))
			run(fixture.Exec("ROLLBACK TO s", 0))
			run(fixture.Exec("DROP FUNCTION d_log() CASCADE", 0))
			run(fixture.Exec("CREATE FUNCTION d_log() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'recreated'); RETURN NULL; END$$", 0))
			run(fixture.Exec(deferredCreate("c", "INSERT", "DEFERRABLE INITIALLY DEFERRED"), 0))
			run(fixture.Exec("INSERT INTO d(id,v) VALUES(5,50)", 1))
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(fixture.Exec("COMMIT", 0))
			run(deferredAudit([]any{"INSERT", int64(2), nil, int64(20), int64(2), "c"}, []any{"INSERT", int64(5), nil, int64(50), int64(0), "recreated"}))
		})
	})
	t.Run("physical-descendant-closure", func(t *testing.T) {
		afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TABLE root(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(100)", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON root DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_log()")
			run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO leaf VALUES(1,10)", 1))
			for _, command := range []string{"DROP TABLE", "TRUNCATE"} {
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.ErrorDiagnostic(command+" root CASCADE", fixture.ControlsReferenceDiagnostic("55006", "cannot "+command+" \"leaf\" because it has pending trigger events")))
				run(fixture.Exec("ROLLBACK TO s", 0))
				run(fixture.Exec("RELEASE s", 0))
				run(deferredCount("leaf", 1))
				run(deferredCount("audit", 0))
			}
			run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
			run(deferredCount("audit", 1))
			run(fixture.Exec("ROLLBACK", 0))
			run(deferredCount("leaf", 0))
			run(deferredCount("audit", 0))
		})
	})
	t.Run("nested-active-ancestor", deferredNestedAncestorDDL)
}
