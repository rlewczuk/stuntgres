package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreRIAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tr_ri_actor", "tr_ri_ref", "tr_ri_fn"})
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c,
			"CREATE ROLE tr_ri_actor", "CREATE ROLE tr_ri_ref", "CREATE ROLE tr_ri_fn",
			"GRANT USAGE ON SCHEMA public TO tr_ri_actor,tr_ri_ref,tr_ri_fn",
			"GRANT CREATE ON SCHEMA public TO tr_ri_fn",
			"CREATE TABLE tr_ri_parent(id integer PRIMARY KEY)",
			"CREATE TABLE tr_ri_child(id integer PRIMARY KEY,pid integer REFERENCES tr_ri_parent(id) ON UPDATE CASCADE ON DELETE CASCADE,v integer)",
			"ALTER TABLE tr_ri_child OWNER TO tr_ri_ref",
			"CREATE TABLE tr_ri_secret(v integer)",
			"GRANT SELECT ON tr_ri_secret TO tr_ri_ref,tr_ri_fn",
			"GRANT SELECT,UPDATE,DELETE ON tr_ri_parent TO tr_ri_actor")
		run(fixture.Exec("INSERT INTO tr_ri_secret VALUES(5)", 1))
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION tr_ri_fn",
			"CREATE FUNCTION tr_ri_helper(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1+(SELECT v FROM public.tr_ri_secret)'",
			"CREATE FUNCTION tr_ri_update() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=public.tr_ri_helper(NEW.v); RETURN NEW; END$$",
			"CREATE FUNCTION tr_ri_delete() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN OLD.v:=public.tr_ri_helper(OLD.v); RETURN OLD; END$$",
			"RESET SESSION AUTHORIZATION",
			"REVOKE SELECT ON tr_ri_secret FROM tr_ri_fn",
			"REVOKE EXECUTE ON FUNCTION tr_ri_helper(integer) FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION tr_ri_helper(integer) TO tr_ri_ref",
			"REVOKE EXECUTE ON FUNCTION tr_ri_update() FROM PUBLIC",
			"REVOKE EXECUTE ON FUNCTION tr_ri_delete() FROM PUBLIC",
			"CREATE TRIGGER u BEFORE UPDATE ON tr_ri_child FOR EACH ROW EXECUTE FUNCTION tr_ri_update()",
			"CREATE TRIGGER d BEFORE DELETE ON tr_ri_child FOR EACH ROW EXECUTE FUNCTION tr_ri_delete()")
		run(fixture.Exec("INSERT INTO tr_ri_parent VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO tr_ri_child VALUES(1,1,10)", 1))
		// Only the referencing owner can both execute the helper and read the
		// secret. Neither the initiator nor the trigger-function owner can.
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION tr_ri_fn")
		run(fixture.Error("SELECT v FROM tr_ri_secret", "42501"))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION tr_ri_actor")
		run(fixture.Error("SELECT tr_ri_helper(1)", "42501"))
		run(fixture.Exec("UPDATE tr_ri_parent SET id=2 WHERE id=1", 1))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(fixture.Select("SELECT id,pid,v FROM tr_ri_child", []string{"id", "pid", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(15)}}))
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION tr_ri_actor")
		run(fixture.Exec("DELETE FROM tr_ri_parent WHERE id=2", 1))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(fixture.Select("SELECT id,pid,v FROM tr_ri_child", []string{"id", "pid", "v"}, []string{"INT4", "INT4", "INT4"}, nil))
		run(fixture.Exec("INSERT INTO tr_ri_parent VALUES(3),(4)", 2))
		run(fixture.Exec("INSERT INTO tr_ri_child VALUES(3,3,30),(4,4,40)", 2))
		unchanged := func() {
			run(fixture.Select("SELECT id FROM tr_ri_parent ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
			run(fixture.Select("SELECT id,pid,v FROM tr_ri_child ORDER BY id", []string{"id", "pid", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(3), int64(30)}, {int64(4), int64(4), int64(40)}}))
		}
		// Give both wrong principals the required rights, then deny only the
		// referencing owner: an initiator/definer authority bug would succeed.
		userOperatorExec(t, p, b, c,
			"GRANT EXECUTE ON FUNCTION tr_ri_helper(integer) TO tr_ri_actor",
			"GRANT SELECT ON tr_ri_secret TO tr_ri_actor,tr_ri_fn",
			"REVOKE EXECUTE ON FUNCTION tr_ri_helper(integer) FROM tr_ri_ref",
			"SET SESSION AUTHORIZATION tr_ri_actor")
		run(fixture.Error("UPDATE tr_ri_parent SET id=id+10", "42501"))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		unchanged()
		userOperatorExec(t, p, b, c,
			"GRANT EXECUTE ON FUNCTION tr_ri_helper(integer) TO tr_ri_ref",
			"REVOKE SELECT ON tr_ri_secret FROM tr_ri_ref",
			"SET SESSION AUTHORIZATION tr_ri_actor")
		run(fixture.Error("DELETE FROM tr_ri_parent", "42501"))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		unchanged()
		userOperatorExec(t, p, b, c, "GRANT SELECT ON tr_ri_secret TO tr_ri_ref", "SET SESSION AUTHORIZATION tr_ri_actor")
		run(fixture.Exec("DELETE FROM tr_ri_parent", 2))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(fixture.Select("SELECT id FROM tr_ri_parent", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT id,pid,v FROM tr_ri_child", []string{"id", "pid", "v"}, []string{"INT4", "INT4", "INT4"}, nil))
	})
}
