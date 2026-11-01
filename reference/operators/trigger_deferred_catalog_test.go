package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDeferredCatalog(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TABLE referenced(id integer)", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d FROM referenced DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION d_log('x''y')", deferredCreate("u", "UPDATE OF v", "DEFERRABLE INITIALLY IMMEDIATE"), deferredCreate("del", "DELETE", "NOT DEFERRABLE"), "CREATE TRIGGER ordinary AFTER INSERT ON d FOR EACH ROW EXECUTE FUNCTION d_log()")
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, deferredOIDCarriers(b, s)) }
		flags := func() {
			run(fixture.Select("SELECT t.tgname,c.conname,c.contype,t.tgtype,t.tgisinternal,t.tgdeferrable,t.tginitdeferred,c.condeferrable,c.condeferred,c.convalidated,c.conenforced,c.conislocal,c.coninhcount,c.connoinherit,c.conparentid FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='d'::regclass ORDER BY t.tgname", []string{"tgname", "conname", "contype", "tgtype", "tgisinternal", "tgdeferrable", "tginitdeferred", "condeferrable", "condeferred", "convalidated", "conenforced", "conislocal", "coninhcount", "connoinherit", "conparentid"}, []string{"NAME", "NAME", "CHAR", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT2", "BOOL", "OID"}, [][]any{
				{"c", "c", "t", int64(5), false, true, true, true, true, true, true, true, int64(0), true, "0"},
				{"del", "del", "t", int64(9), false, false, false, false, false, true, true, true, int64(0), true, "0"},
				{"u", "u", "t", int64(17), false, true, false, true, false, true, true, true, int64(0), true, "0"},
			}))
		}
		flags()
		run(fixture.Select("SELECT t.tgname,t.oid<>c.oid AS distinct_ids,t.oid>0 AND c.oid>0 AS valid_ids,c.conrelid=t.tgrelid AS same_table,c.connamespace=n.oid AS same_schema,t.tgconstraint=c.oid AS linked,t.tgconstrindid,c.conindid,c.confrelid,c.contypid,t.tgconstrrelid='referenced'::regclass AS from_ref,c.conkey IS NULL AS null_key,c.confkey IS NULL AS null_fkey,c.conbin IS NULL AS null_expr,c.conpfeqop IS NULL AS null_eq,c.conppeqop IS NULL AS null_peq,c.conffeqop IS NULL AS null_feq,c.confdelsetcols IS NULL AS null_set FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint JOIN pg_namespace n ON n.nspname='public' WHERE t.tgrelid='d'::regclass AND t.tgname='c'", []string{"tgname", "distinct_ids", "valid_ids", "same_table", "same_schema", "linked", "tgconstrindid", "conindid", "confrelid", "contypid", "from_ref", "null_key", "null_fkey", "null_expr", "null_eq", "null_peq", "null_feq", "null_set"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "OID", "OID", "OID", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"c", true, true, true, true, true, "0", "0", "0", "0", true, true, true, true, true, true, true, true}}))
		run(fixture.Select("SELECT tgname,tgconstraint,tgconstrrelid,tgconstrindid,tgdeferrable,tginitdeferred,tgisinternal FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname='ordinary'", []string{"tgname", "tgconstraint", "tgconstrrelid", "tgconstrindid", "tgdeferrable", "tginitdeferred", "tgisinternal"}, []string{"NAME", "OID", "OID", "OID", "BOOL", "BOOL", "BOOL"}, [][]any{{"ordinary", "0", "0", "0", false, false, false}}))
		run(fixture.Select("SELECT tgname,tgconstrrelid,tgconstrindid FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname IN('del','u') ORDER BY tgname", []string{"tgname", "tgconstrrelid", "tgconstrindid"}, []string{"NAME", "OID", "OID"}, [][]any{{"del", "0", "0"}, {"u", "0", "0"}}))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SET CONSTRAINTS c,u IMMEDIATE", 0))
		flags()
		run(fixture.Exec("SET CONSTRAINTS ALL DEFERRED", 0))
		flags()
		run(fixture.Exec("ROLLBACK", 0))
		// Formatting observed on pinned PostgreSQL18.6, including FROM visibility.
		run(fixture.Select("SELECT pg_get_triggerdef(oid) AS definition,pg_get_triggerdef(oid,true) AS pretty FROM pg_trigger WHERE tgrelid='d'::regclass AND tgname='c'", []string{"definition", "pretty"}, []string{"TEXT", "TEXT"}, [][]any{{"CREATE CONSTRAINT TRIGGER c AFTER INSERT ON public.d FROM referenced DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN ((new.v > 0)) EXECUTE FUNCTION d_log('x''y')", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d FROM referenced DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (new.v > 0) EXECUTE FUNCTION d_log('x''y')"}}))
		run(fixture.Select("SELECT trigger_name,event_manipulation,action_timing,action_orientation,action_condition,action_statement FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='d' AND trigger_name='c'", []string{"trigger_name", "event_manipulation", "action_timing", "action_orientation", "action_condition", "action_statement"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"c", "INSERT", "AFTER", "ROW", "(new.v > 0)", "EXECUTE FUNCTION d_log('x''y')"}}))
		run(fixture.Select("SELECT count(*) AS n FROM information_schema.table_constraints WHERE table_schema='public' AND table_name='d' AND constraint_name IN('c','del','u')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT dep.deptype FROM pg_trigger t JOIN pg_depend dep ON dep.classid='pg_constraint'::regclass AND dep.objid=t.tgconstraint AND dep.refclassid='pg_trigger'::regclass AND dep.refobjid=t.oid WHERE t.tgrelid='d'::regclass AND t.tgname='c'", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}))
		run(fixture.Exec("ALTER TRIGGER c ON d RENAME TO live", 0))
		run(fixture.Exec("ALTER TABLE d RENAME CONSTRAINT c TO selected", 0))
		run(fixture.Select("SELECT t.tgname,c.conname FROM pg_trigger t JOIN pg_constraint c ON c.oid=t.tgconstraint WHERE t.tgrelid='d'::regclass AND t.tgname='live'", []string{"tgname", "conname"}, []string{"NAME", "NAME"}, [][]any{{"live", "selected"}}))
	})
	t.Run("real-clone-links", func(t *testing.T) {
		triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			deferredSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TABLE root(id integer,v integer) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF root FOR VALUES FROM(0) TO(100)", "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON root DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION d_log()")
			q := "SELECT child.tgparentid=parent.oid AS lineage,child.oid<>parent.oid AS distinct_trigger,cc.oid<>pc.oid AS distinct_constraint,cc.conparentid,cc.conislocal,cc.coninhcount,cc.connoinherit,cc.condeferrable,cc.condeferred FROM pg_trigger child JOIN pg_trigger parent ON parent.tgrelid='root'::regclass AND parent.tgname='c' JOIN pg_constraint cc ON cc.oid=child.tgconstraint JOIN pg_constraint pc ON pc.oid=parent.tgconstraint WHERE child.tgrelid='leaf'::regclass AND child.tgname='c'"
			s := fixture.Select(q, []string{"lineage", "distinct_trigger", "distinct_constraint", "conparentid", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred"}, []string{"BOOL", "BOOL", "BOOL", "OID", "BOOL", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, "0", true, int64(0), true, true, true}})
			s = deferredOIDCarriers(b, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
	t.Run("visibility", deferredCatalogVisibility)
}

func deferredCatalogVisibility(t *testing.T) {
	afterRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
		fixture.RoleReferenceCleanup(t, []string{"d_catalog_reader"})
		deferredSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE CONSTRAINT TRIGGER c AFTER INSERT ON d DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN(NEW.v>0) EXECUTE FUNCTION d_log()", "CREATE ROLE d_catalog_reader", "GRANT USAGE ON SCHEMA public TO d_catalog_reader", "GRANT SELECT ON d TO d_catalog_reader")
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE ALL PRIVILEGES ON TABLE d FROM d_catalog_reader", "REVOKE ALL PRIVILEGES ON SCHEMA public FROM d_catalog_reader"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION d_catalog_reader", 0))
		run(fixture.Select("SELECT count(*) AS n FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='d'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT TRIGGER ON d TO d_catalog_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION d_catalog_reader", 0))
		run(fixture.Select("SELECT trigger_name,action_orientation,action_timing,action_condition FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='d'", []string{"trigger_name", "action_orientation", "action_timing", "action_condition"}, []string{"NAME", "VARCHAR", "VARCHAR", "VARCHAR"}, [][]any{{"c", "ROW", "AFTER", nil}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
