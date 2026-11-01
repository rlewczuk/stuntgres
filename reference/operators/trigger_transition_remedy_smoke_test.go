package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerTransitionRemedySmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tt_sm_actor", "tt_sm_owner"})
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		userOperatorExec(t, p, b, c,
			"CREATE TABLE sm_parent(id integer,v integer)", "CREATE TABLE sm_child(extra text) INHERITS(sm_parent)",
			"CREATE TABLE sm_images(phase text,row_text text)",
			"CREATE FUNCTION sm_old() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sm_images SELECT TG_TABLE_NAME||'-OLD',x::text FROM x; RETURN NULL; END$$",
			"CREATE FUNCTION sm_new() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sm_images SELECT TG_TABLE_NAME||'-NEW',x::text FROM x; RETURN NULL; END$$",
			"CREATE FUNCTION sm_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sm_images SELECT 'move-OLD',y::text FROM y; INSERT INTO sm_images SELECT 'move-NEW',x::text FROM x; RETURN NULL; END$$",
			"CREATE TRIGGER d AFTER DELETE ON sm_parent REFERENCING OLD TABLE x EXECUTE FUNCTION sm_old()",
			"CREATE TABLE sm_part(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE sm_lo PARTITION OF sm_part FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE sm_hi(v integer,id integer)", "ALTER TABLE sm_part ATTACH PARTITION sm_hi FOR VALUES FROM(10) TO(30)",
			"CREATE TRIGGER i AFTER INSERT ON sm_part REFERENCING NEW TABLE x EXECUTE FUNCTION sm_new()",
			"CREATE TRIGGER u AFTER UPDATE ON sm_part REFERENCING OLD TABLE y NEW TABLE x EXECUTE FUNCTION sm_update()",
			"CREATE TABLE sm_null(id integer,v integer)", "CREATE TABLE sm_presence(k integer,row_text text)",
			"CREATE FUNCTION sm_presence_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sm_presence SELECT v.k,n::text FROM (VALUES(0),(1)) v(k) LEFT JOIN (x n CROSS JOIN LATERAL generate_series(1,v.k) g(i)) ON true; RETURN NULL; END$$",
			"CREATE TRIGGER i AFTER INSERT ON sm_null REFERENCING NEW TABLE x EXECUTE FUNCTION sm_presence_body()")
		run(fixture.Exec("INSERT INTO sm_parent VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO sm_child VALUES(2,20,'extra')", 1))
		run(fixture.Exec("DELETE FROM sm_parent", 2))
		run(fixture.Exec("INSERT INTO sm_part VALUES(1,10),(2,20)", 2))
		run(fixture.Exec("UPDATE sm_part SET id=11 WHERE id=1", 1))
		run(fixture.Exec("INSERT INTO sm_null VALUES(NULL,NULL)", 1))
		s := fixture.Select("SELECT phase,row_text FROM sm_images", []string{"phase", "row_text"}, []string{"TEXT", "TEXT"}, [][]any{
			{"sm_parent-OLD", "(1,10)"}, {"sm_parent-OLD", "(2,20)"}, {"sm_part-NEW", "(1,10)"}, {"sm_part-NEW", "(2,20)"}, {"move-OLD", "(1,10)"}, {"move-NEW", "(11,10)"},
		})
		s.Bag = true
		run(s)
		run(fixture.Select("SELECT k,row_text FROM sm_presence ORDER BY k", []string{"k", "row_text"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), nil}, {int64(1), "(,)"}}))
		userOperatorExec(t, p, b, c, "CREATE ROLE tt_sm_actor")
		defer userOperatorExec(t, p, b, c, "RESET ROLE", "DROP OWNED BY tt_sm_actor", "DROP ROLE tt_sm_actor")
		userOperatorExec(t, p, b, c, "CREATE ROLE tt_sm_owner")
		defer userOperatorExec(t, p, b, c, "RESET ROLE", "DROP OWNED BY tt_sm_owner", "DROP ROLE tt_sm_owner")
		userOperatorExec(t, p, b, c,
			"GRANT USAGE ON SCHEMA public TO tt_sm_actor,tt_sm_owner", "GRANT CREATE ON SCHEMA public TO tt_sm_owner",
			"CREATE TABLE sm_ri_parent(id integer PRIMARY KEY)", "GRANT REFERENCES ON sm_ri_parent TO tt_sm_owner", "SET ROLE tt_sm_owner",
			"CREATE TABLE sm_ri_child(id integer,pid integer REFERENCES sm_ri_parent ON DELETE CASCADE)", "RESET ROLE",
			"CREATE TABLE sm_ri_log(actor text,depth integer,id integer,n bigint)",
			"CREATE FUNCTION sm_ri_body() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO sm_ri_log SELECT current_user::text,pg_trigger_depth(),id,(SELECT count(*) FROM x) FROM x; RETURN NULL; END$$",
			"CREATE TRIGGER r AFTER DELETE ON sm_ri_child REFERENCING OLD TABLE x FOR EACH ROW EXECUTE FUNCTION sm_ri_body()",
			"GRANT INSERT ON sm_ri_log TO tt_sm_owner", "GRANT DELETE ON sm_ri_parent TO tt_sm_actor")
		run(fixture.Exec("INSERT INTO sm_ri_parent VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO sm_ri_child VALUES(11,1)", 1))
		userOperatorExec(t, p, b, c, "SET ROLE tt_sm_actor")
		run(fixture.Exec("DELETE FROM sm_ri_parent", 1))
		run(fixture.Select("SELECT current_user::text AS actor,pg_trigger_depth() AS depth", []string{"actor", "depth"}, []string{"TEXT", "INT4"}, [][]any{{"tt_sm_actor", int64(0)}}))
		userOperatorExec(t, p, b, c, "RESET ROLE")
		run(fixture.Select("SELECT actor,depth,id,n FROM sm_ri_log", []string{"actor", "depth", "id", "n"}, []string{"TEXT", "INT4", "INT4", "INT8"}, [][]any{{"tt_sm_owner", int64(1), int64(11), int64(1)}}))
	})
}
