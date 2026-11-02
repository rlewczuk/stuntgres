package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLUpdatePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbupd_reader", "dbupd_writer", "dbupd_column"})
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		updateKeys(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE dbupd_reader", "CREATE ROLE dbupd_writer", "CREATE ROLE dbupd_column",
			"GRANT USAGE ON SCHEMA public,dbupd_ext TO dbupd_reader,dbupd_writer,dbupd_column",
			"GRANT SELECT ON update_keys TO dbupd_reader", "GRANT UPDATE ON update_keys TO dbupd_writer", "GRANT SELECT(id) ON update_keys TO dbupd_column",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		held := updateHeld(t, p, c, updateCall)
		check := func(s fixture.Step) {
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		good := updateSelect(updateCall, "UPDATE update_keys SET id = '9', k = 'a', payload = 'O''Brien' WHERE id = '9'", "update_keys", "1", int64(1), "{1}", "{9}")
		denied := updateError(updateCall, "42501", "permission denied for table update_keys", "update_keys", "0", int64(0), "{}", "{}")
		run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_reader", 0))
		check(good)
		run(fixture.Select("SELECT has_table_privilege(current_user,'update_keys','UPDATE') AS can_update", []string{"can_update"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"dbupd_writer", "dbupd_column"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(denied)
			check(updateSelect(updateCall, nil, "update_keys", "0", int64(0), "{}", nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT ON update_keys TO dbupd_writer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_writer", 0))
		check(good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON update_keys FROM dbupd_writer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_writer", 0))
		check(denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{
			"ALTER TABLE update_keys ENABLE ROW LEVEL SECURITY",
			"CREATE POLICY update_visible ON update_keys FOR SELECT TO dbupd_reader USING(id=2)",
			"SET SESSION AUTHORIZATION dbupd_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		check(updateError(updateCall, "21000", "source row not found", "update_keys", "1", int64(1), "{1}", "{9}"))
		check(updateSelect(updateCall, "UPDATE update_keys SET id = '9', k = NULL, payload = NULL WHERE id = '9'", "update_keys", "1", int64(1), "{2}", "{9}"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER POLICY update_visible ON update_keys USING(id>=2)", 0))
		run(fixture.Exec("INSERT INTO update_keys VALUES(2,NULL,'visible duplicate')", 1))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_reader", 0))
		check(updateError(updateCall, "21000", "source criteria matched more than one record", "update_keys", "1", int64(1), "{2}", "{9}"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE update_keys DISABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION dbupd_ext.dblink_build_sql_update"+updateSignature+" FROM PUBLIC", 0))
		const child = "SELECT dbupd_ext.dblink_build_sql_update((1/(pg_backend_pid()*0))::text,'1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
		childHeld := updateHeld(t, p, c, child)
		run(fixture.Exec("CREATE TABLE update_nullable(keys text[])", 0))
		run(fixture.Exec("INSERT INTO update_nullable VALUES(NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON update_nullable TO dbupd_reader", 0))
		const dynamicNull = "SELECT dbupd_ext.dblink_build_sql_update('missing_update_relation','1'::int2vector,1,keys,'{9}'::text[]) AS sql FROM update_nullable"
		dynamicHeld := updateHeld(t, p, c, dynamicNull)
		for _, allowed := range []bool{false, true, false} {
			q := "REVOKE EXECUTE ON FUNCTION dbupd_ext.dblink_build_sql_update" + updateSignature + " FROM dbupd_reader"
			if allowed {
				q = "GRANT EXECUTE ON FUNCTION dbupd_ext.dblink_build_sql_update" + updateSignature + " TO dbupd_reader"
			}
			run(fixture.Exec(q, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_reader", 0))
			code, message := "42501", "permission denied for function dblink_build_sql_update"
			if allowed {
				code, message = "22012", "division by zero"
			}
			s := updateError(child, code, message)
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, childHeld, b.Recorder, s)
			if allowed {
				check(good)
			} else {
				check(updateError(updateCall, code, message, "update_keys", "1", int64(1), "{1}", "{9}"))
			}
			if allowed {
				updateBoth(t, p, b, c, updateSelect(dynamicNull, nil))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, updateSelect(dynamicNull, nil))
			} else {
				updateBoth(t, p, b, c, updateError(dynamicNull, code, message))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, updateError(dynamicNull, code, message))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dbupd_ext.dblink_build_sql_update"+updateSignature+" TO dbupd_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA dbupd_ext FROM dbupd_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbupd_reader", 0))
		s := fixture.ImplicitError(updateCall, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema dbupd_ext", Position: "8"}, "update_keys", "1", int64(1), "{1}", "{9}")
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		heldSchema := s
		heldSchema.Prepared, heldSchema.ErrorStage = false, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, heldSchema)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferenceDblinkBuildSQLUpdateIdentity(t *testing.T) {
	updateCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA dbupd_left", "CREATE SCHEMA dbupd_right",
			"CREATE TABLE dbupd_left.update_same(left_id integer)", "CREATE TABLE dbupd_right.update_same(right_id integer)",
			"CREATE TABLE update_live(gone text,id integer,payload text)", "ALTER TABLE update_live DROP COLUMN gone",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"INSERT INTO dbupd_left.update_same VALUES(1)", "INSERT INTO dbupd_right.update_same VALUES(1)", "INSERT INTO update_live VALUES(1,'live')"} {
			run(fixture.Exec(q, 1))
		}
		held := updateHeld(t, p, c, updateCall)
		check := func(relation, want string) {
			s := updateSelect(updateCall, want, relation, "1", int64(1), "{1}", "{9}")
			updateBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET search_path=dbupd_left,dbupd_right,public", 0))
		check("update_same", "UPDATE update_same SET left_id = '9' WHERE left_id = '9'")
		check("dbupd_left.update_same", "UPDATE update_same SET left_id = '9' WHERE left_id = '9'")
		check("dbupd_right.update_same", "UPDATE dbupd_right.update_same SET right_id = '9' WHERE right_id = '9'")
		run(fixture.Exec("SET search_path=dbupd_right,dbupd_left,public", 0))
		check("update_same", "UPDATE update_same SET right_id = '9' WHERE right_id = '9'")
		check("dbupd_left.update_same", "UPDATE dbupd_left.update_same SET left_id = '9' WHERE left_id = '9'")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("dbupd_left.update_same", `UPDATE "dbupd_left"."update_same" SET "left_id" = '9' WHERE "left_id" = '9'`)
		run(fixture.Exec("SET quote_all_identifiers=off", 0))
		run(fixture.Exec("SET search_path=public", 0))
		check("update_live", "UPDATE update_live SET id = '9', payload = 'live' WHERE id = '9'")
		run(fixture.Exec("CREATE VIEW update_saved AS SELECT dbupd_ext.dblink_build_sql_update('public.update_live','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql", 0))
		run(fixture.Exec("ALTER TABLE update_live RENAME COLUMN id TO current_id", 0))
		check("update_live", "UPDATE update_live SET current_id = '9', payload = 'live' WHERE current_id = '9'")
		updateBoth(t, p, b, c, updateSelect("SELECT sql FROM update_saved", "UPDATE update_live SET current_id = '9', payload = 'live' WHERE current_id = '9'"))
		run(fixture.Exec("ALTER TABLE update_live RENAME TO update_old", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, updateError(updateCall, "42P01", `relation "update_live" does not exist`, "update_live", "1", int64(1), "{1}", "{9}"))
		run(fixture.Exec("CREATE TABLE update_live(replacement_id integer)", 0))
		run(fixture.Exec("INSERT INTO update_live VALUES(1)", 1))
		check("update_live", "UPDATE update_live SET replacement_id = '9' WHERE replacement_id = '9'")
		updateBoth(t, p, b, c, updateSelect("SELECT sql FROM update_saved", "UPDATE update_live SET replacement_id = '9' WHERE replacement_id = '9'"))
		run(fixture.Exec("DROP TABLE update_live", 0))
		run(fixture.Exec("CREATE TABLE update_live(third_id integer)", 0))
		run(fixture.Exec("INSERT INTO update_live VALUES(1)", 1))
		check("update_live", "UPDATE update_live SET third_id = '9' WHERE third_id = '9'")
		// Output functions use current settings; replacement TEXT is not
		// reformatted as a date, numeric, boolean or binary value.
		run(fixture.Exec("CREATE TABLE update_output(id integer,n numeric,b boolean,d date,bytes bytea)", 0))
		run(fixture.Exec("INSERT INTO update_output VALUES(1,12.50,true,DATE '2024-03-09',decode('5c27','hex'))", 1))
		for _, tc := range []struct{ setting, date, bytes string }{
			{"SET DateStyle='ISO, MDY'", "2024-03-09", `E'\\x5c27'`},
			{"SET DateStyle='SQL, DMY'", "09/03/2024", `E'\\x5c27'`},
		} {
			run(fixture.Exec(tc.setting, 0))
			updateBoth(t, p, b, c, updateSelect(updateCall, "UPDATE update_output SET id = '009', n = '12.50', b = 't', d = '"+tc.date+"', bytes = "+tc.bytes+" WHERE id = '009'", "update_output", "1", int64(1), "{1}", "{009}"))
		}
		run(fixture.Exec("SET DateStyle='ISO, MDY'", 0))
		run(fixture.Exec("SET bytea_output='escape'", 0))
		updateBoth(t, p, b, c, updateSelect(updateCall, `UPDATE update_output SET id = '009', n = '12.50', b = 't', d = '2024-03-09', bytes = E'\\\\''' WHERE id = '009'`, "update_output", "1", int64(1), "{1}", "{009}"))
	})
}
