package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func deferredNestedAncestorDDL(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		name := "set-recovery"
		if terminal {
			name = "commit-recovery"
		}
		t.Run(name, func(t *testing.T) {
			afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
				deferredSetup(t, p, b, c)
				userOperatorExec(t, p, b, c,
					"CREATE TABLE ddl_child(id integer)",
					"CREATE FUNCTION d_ancestor_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 7'",
					"CREATE FUNCTION d_nested_ddl_a() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NEW.v,0,'outer'); CREATE OR REPLACE FUNCTION d_ancestor_helper() RETURNS integer LANGUAGE SQL AS 'SELECT 99'; INSERT INTO ddl_child VALUES(NEW.id); RETURN NULL; END$$",
					"CREATE FUNCTION d_nested_ddl_b() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NULL,0,'inner'); ALTER TABLE d ADD COLUMN forbidden integer; RETURN NULL; END$$",
					"CREATE CONSTRAINT TRIGGER ancestor AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_nested_ddl_a()",
					"CREATE CONSTRAINT TRIGGER nested AFTER INSERT ON ddl_child NOT DEFERRABLE FOR EACH ROW EXECUTE FUNCTION d_nested_ddl_b()")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				unchanged := func(rows int64) {
					run(deferredCount("d", rows))
					run(deferredCount("audit", 0))
					run(deferredCount("ddl_child", 0))
					run(fixture.Select("SELECT count(*) AS n FROM pg_attribute WHERE attrelid='d'::regclass AND attname='forbidden' AND NOT attisdropped", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT d_ancestor_helper() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Select("SELECT t.tgname,c.conname,t.tgdeferrable,t.tginitdeferred,c.conrelid=t.tgrelid AND c.oid<>t.oid AS linked FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid IN('d'::regclass,'ddl_child'::regclass) ORDER BY t.tgname", []string{"tgname", "conname", "tgdeferrable", "tginitdeferred", "linked"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"ancestor", "ancestor", true, true, true}, {"nested", "nested", false, false, true}}))
				}
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO d(id,v) VALUES(1,10)", 1))
				unchanged(1)
				command := "SET CONSTRAINTS ALL IMMEDIATE"
				if terminal {
					command = "COMMIT"
				} else {
					run(fixture.Exec("SAVEPOINT s", 0))
				}
				// CheckTableNotInUse checks active relation use before unfinished
				// events. The full nested diagnostic is an independent released-source
				// prediction until root observes this new witness on pinned PostgreSQL.
				d := fixture.ControlsReferenceDiagnostic("55006", `cannot ALTER TABLE "d" because it is being used by active queries in this session`)
				d.Context = "SQL statement \"ALTER TABLE d ADD COLUMN forbidden integer\"\nPL/pgSQL function d_nested_ddl_b() line 1 at SQL statement\nSQL statement \"INSERT INTO ddl_child VALUES(NEW.id)\"\nPL/pgSQL function d_nested_ddl_a() line 1 at SQL statement"
				run(fixture.ErrorDiagnostic(command, d))
				if terminal {
					unchanged(0)
				} else {
					run(deferredAborted("SELECT 1"))
					run(fixture.Exec("ROLLBACK TO s", 0))
					unchanged(1)
				}
				run(fixture.Exec("CREATE OR REPLACE FUNCTION d_nested_ddl_b() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO audit VALUES(TG_OP,NEW.id,NULL,NULL,0,'inner'); RETURN NULL; END$$", 0))
				id := int64(1)
				if terminal {
					id = 2
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO d(id,v) VALUES(2,20)", 1))
				}
				run(fixture.Exec("SET CONSTRAINTS ALL IMMEDIATE", 0))
				run(deferredAudit([]any{"INSERT", id, nil, nil, int64(0), "inner"}, []any{"INSERT", id, nil, id * 10, int64(0), "outer"}))
				run(deferredCount("ddl_child", 1))
				run(fixture.Exec("COMMIT", 0))
				run(deferredCount("audit", 2))
				run(fixture.Prepared(fixture.Select("SELECT d_ancestor_helper() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}})))
			})
		})
	}
}
