package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerCoreLifetime(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tr_lifetime")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Error(err)
			}
		}()
		userOperatorExec(t, p, b, c, "CREATE SCHEMA tr_lifetime", "SET search_path=tr_lifetime,public")
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Error("DROP FUNCTION tr_double()", "2BP01"))
		userOperatorExec(t, p, b, c, "ALTER FUNCTION tr_double() RENAME TO tr_renamed")
		run(fixture.Select("SELECT count(*) AS v FROM pg_proc WHERE proname='tr_renamed'", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
		userOperatorExec(t, p, b, c, "ALTER TABLE tr_data RENAME COLUMN v TO amount")
		// Member lookup is late PL binding, so update the relation member name
		// again before invoking the unchanged body.
		userOperatorExec(t, p, b, c, "ALTER TABLE tr_data RENAME COLUMN amount TO v", "ALTER TABLE tr_data RENAME TO tr_renamed_table")
		// Reusing the original function name must not retarget the installed
		// trigger. The decoy triples; the renamed installed function doubles.
		userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*3; RETURN NEW; END$$")
		run(triggerRows("INSERT INTO tr_renamed_table VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
		run(fixture.Select("SELECT relhastriggers FROM pg_class WHERE oid='tr_renamed_table'::regclass", []string{"relhastriggers"}, []string{"BOOL"}, [][]any{{true}}))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET search_path=tr_lifetime,public", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, triggerRows("SELECT id,v FROM tr_renamed_table WHERE id=1", []any{int64(1), int64(10)}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, triggerRows("INSERT INTO tr_renamed_table VALUES(99,5) RETURNING id,v", []any{int64(99), int64(10)}))
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ROLLBACK", 0))
		}
		parametersReplay(t, p, b, c, "tr_lifetime", check)
		run(fixture.Error("DROP FUNCTION tr_renamed()", "2BP01"))
		userOperatorExec(t, p, b, c, "DROP FUNCTION tr_renamed() CASCADE", "DROP FUNCTION tr_double()", "CREATE FUNCTION tr_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := NEW.v*3; RETURN NEW; END$$")
		run(triggerRows("INSERT INTO tr_renamed_table VALUES(2,5) RETURNING id,v", []any{int64(2), int64(5)}))
		userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_renamed_table FOR EACH ROW EXECUTE FUNCTION tr_double()")
		run(triggerRows("INSERT INTO tr_renamed_table VALUES(3,5) RETURNING id,v", []any{int64(3), int64(15)}))
		userOperatorExec(t, p, b, c, "DROP TABLE tr_renamed_table", "DROP FUNCTION tr_double()")
	})
}

func TestReferenceTriggerCoreRLS(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tr_rls_actor"})
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE ROLE tr_rls_actor", "GRANT USAGE ON SCHEMA public TO tr_rls_actor", "GRANT SELECT,INSERT,UPDATE ON tr_data TO tr_rls_actor", "ALTER TABLE tr_data ENABLE ROW LEVEL SECURITY", "CREATE POLICY tr_read ON tr_data FOR SELECT USING(id<10)", "CREATE POLICY tr_insert ON tr_data FOR INSERT WITH CHECK(v=10)", "CREATE POLICY tr_update ON tr_data FOR UPDATE USING(id<10) WITH CHECK(v=10)", "CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "SET SESSION AUTHORIZATION tr_rls_actor")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
		run(fixture.Error("INSERT INTO tr_data VALUES(2,10)", "42501"))
		run(fixture.Exec("UPDATE tr_data SET v=5 WHERE id=1", 1))
		run(fixture.Error("UPDATE tr_data SET v=10 WHERE id=1", "42501"))
		userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("INSERT INTO tr_data VALUES(11,5)", 1))
		userOperatorExec(t, p, b, c, "SET SESSION AUTHORIZATION tr_rls_actor")
		run(fixture.Exec("UPDATE tr_data SET v=5 WHERE id=11", 0))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(1), int64(10)}))
	})
}
