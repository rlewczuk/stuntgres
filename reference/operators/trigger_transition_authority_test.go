package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerTransitionRIExposureBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tt_ri_actor", "tt_ri_owner"})
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		userOperatorExec(t, p, b, c, "CREATE ROLE tt_ri_actor")
		defer userOperatorExec(t, p, b, c, "RESET ROLE", "DROP OWNED BY tt_ri_actor", "DROP ROLE tt_ri_actor")
		userOperatorExec(t, p, b, c, "CREATE ROLE tt_ri_owner")
		defer userOperatorExec(t, p, b, c, "RESET ROLE", "DROP OWNED BY tt_ri_owner", "DROP ROLE tt_ri_owner")
		userOperatorExec(t, p, b, c,
			"CREATE TABLE tt_ri_parent(id integer PRIMARY KEY)",
			"GRANT USAGE ON SCHEMA public TO tt_ri_actor,tt_ri_owner", "GRANT CREATE ON SCHEMA public TO tt_ri_owner", "GRANT REFERENCES ON tt_ri_parent TO tt_ri_owner", "SET ROLE tt_ri_owner",
			"CREATE TABLE tt_ri_child(id integer,pid integer REFERENCES tt_ri_parent ON DELETE CASCADE)",
			"RESET ROLE",
			"CREATE TABLE tt_ri_log(phase text,actor text,depth integer,id integer,n bigint)",
			"CREATE FUNCTION tt_ri_qualify() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_ri_log VALUES('qualify',current_user::text,pg_trigger_depth(),NULL,NULL); RETURN true; END$$",
			"CREATE FUNCTION tt_ri_qualify_fail() RETURNS boolean LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN true; END$$",
			"CREATE FUNCTION tt_ri_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_ri_log VALUES(TG_LEVEL,current_user::text,pg_trigger_depth(),OLD.id,(SELECT count(*) FROM x)); RETURN NULL; END$$",
			"CREATE FUNCTION tt_ri_before() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_ri_log VALUES('before',current_user::text,pg_trigger_depth(),OLD.id,NULL); RETURN OLD; END$$",
			"CREATE FUNCTION tt_ri_parent_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO tt_ri_log VALUES('parent',current_user::text,pg_trigger_depth(),OLD.id,NULL); RETURN NULL; END$$",
			"CREATE TRIGGER b BEFORE DELETE ON tt_ri_child FOR EACH ROW EXECUTE FUNCTION tt_ri_before()",
			"CREATE TRIGGER r AFTER DELETE ON tt_ri_child REFERENCING OLD TABLE x FOR EACH ROW WHEN(tt_ri_qualify()) EXECUTE FUNCTION tt_ri_body()",
			"CREATE TRIGGER s AFTER DELETE ON tt_ri_child REFERENCING OLD TABLE x FOR EACH STATEMENT WHEN(tt_ri_qualify()) EXECUTE FUNCTION tt_ri_body()",
			"CREATE TRIGGER r AFTER DELETE ON tt_ri_parent FOR EACH ROW EXECUTE FUNCTION tt_ri_parent_body()",
			"GRANT SELECT,DELETE ON tt_ri_parent TO tt_ri_actor", "GRANT INSERT ON tt_ri_log TO tt_ri_actor,tt_ri_owner")
		run(fixture.Exec("INSERT INTO tt_ri_parent VALUES(1),(2)", 2))
		run(fixture.Exec("INSERT INTO tt_ri_child VALUES(11,1),(12,1),(21,2)", 3))
		userOperatorExec(t, p, b, c, "SET ROLE tt_ri_actor")
		run(fixture.Exec("DELETE FROM tt_ri_parent WHERE id=1", 1))
		run(fixture.Select("SELECT current_user::text AS actor,pg_trigger_depth() AS depth", []string{"actor", "depth"}, []string{"TEXT", "INT4"}, [][]any{{"tt_ri_actor", int64(0)}}))
		userOperatorExec(t, p, b, c, "RESET ROLE")
		s := fixture.Select("SELECT phase,actor,depth,id,n FROM tt_ri_log", []string{"phase", "actor", "depth", "id", "n"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT8"}, [][]any{
			{"qualify", "tt_ri_owner", int64(1), nil, nil}, {"before", "tt_ri_owner", int64(2), int64(11), nil}, {"before", "tt_ri_owner", int64(2), int64(12), nil},
			{"qualify", "tt_ri_owner", int64(1), nil, nil}, {"qualify", "tt_ri_owner", int64(1), nil, nil},
			{"ROW", "tt_ri_owner", int64(1), int64(11), int64(2)}, {"ROW", "tt_ri_owner", int64(1), int64(12), int64(2)}, {"STATEMENT", "tt_ri_owner", int64(1), nil, int64(2)}, {"parent", "tt_ri_actor", int64(1), int64(1), nil},
		})
		s.Bag = true
		run(s)
		userOperatorExec(t, p, b, c,
			"TRUNCATE tt_ri_log",
			"CREATE FUNCTION tt_ri_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
			"CREATE TRIGGER z AFTER DELETE ON tt_ri_child REFERENCING OLD TABLE x FOR EACH ROW EXECUTE FUNCTION tt_ri_fail()", "SET ROLE tt_ri_actor")
		want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		want.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function tt_ri_fail() line 1 at PERFORM"
		run(fixture.ErrorDiagnostic("DELETE FROM tt_ri_parent WHERE id=2", want))
		run(fixture.Select("SELECT current_user::text AS actor,pg_trigger_depth() AS depth", []string{"actor", "depth"}, []string{"TEXT", "INT4"}, [][]any{{"tt_ri_actor", int64(0)}}))
		userOperatorExec(t, p, b, c, "RESET ROLE")
		run(fixture.Select("SELECT id FROM tt_ri_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Select("SELECT id,pid FROM tt_ri_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(21), int64(2)}}))
		run(fixture.Select("SELECT phase FROM tt_ri_log", []string{"phase"}, []string{"TEXT"}, nil))
		userOperatorExec(t, p, b, c, "DROP TRIGGER z ON tt_ri_child",
			"CREATE OR REPLACE TRIGGER r AFTER DELETE ON tt_ri_child REFERENCING OLD TABLE x FOR EACH ROW WHEN(tt_ri_qualify_fail()) EXECUTE FUNCTION tt_ri_body()", "SET ROLE tt_ri_actor")
		want.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function tt_ri_qualify_fail() line 1 at PERFORM\nSQL statement \"DELETE FROM ONLY \"public\".\"tt_ri_child\" WHERE $1 OPERATOR(pg_catalog.=) \"pid\"\""
		run(fixture.ErrorDiagnostic("DELETE FROM tt_ri_parent WHERE id=2", want))
		run(fixture.Select("SELECT current_user::text AS actor,pg_trigger_depth() AS depth", []string{"actor", "depth"}, []string{"TEXT", "INT4"}, [][]any{{"tt_ri_actor", int64(0)}}))
		userOperatorExec(t, p, b, c, "RESET ROLE")
		run(fixture.Select("SELECT id FROM tt_ri_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Select("SELECT id,pid FROM tt_ri_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(21), int64(2)}}))
		run(fixture.Select("SELECT phase FROM tt_ri_log", []string{"phase"}, []string{"TEXT"}, nil))
		userOperatorExec(t, p, b, c,
			"CREATE OR REPLACE TRIGGER r AFTER DELETE ON tt_ri_child REFERENCING OLD TABLE x FOR EACH ROW WHEN(tt_ri_qualify()) EXECUTE FUNCTION tt_ri_body()", "SET ROLE tt_ri_actor")
		run(fixture.Exec("DELETE FROM tt_ri_parent WHERE id=2", 1))
		userOperatorExec(t, p, b, c, "RESET ROLE")
		s = fixture.Select("SELECT phase,actor,depth,id,n FROM tt_ri_log", []string{"phase", "actor", "depth", "id", "n"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT8"}, [][]any{
			{"qualify", "tt_ri_owner", int64(1), nil, nil}, {"before", "tt_ri_owner", int64(2), int64(21), nil}, {"ROW", "tt_ri_owner", int64(1), int64(21), int64(1)}, {"STATEMENT", "tt_ri_owner", int64(1), nil, int64(1)}, {"parent", "tt_ri_actor", int64(1), int64(2), nil},
			{"qualify", "tt_ri_owner", int64(1), nil, nil},
		})
		s.Bag = true
		run(s)
	})
}
