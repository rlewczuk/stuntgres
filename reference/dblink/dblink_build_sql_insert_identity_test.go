package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLInsertPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbins_reader", "dbins_writer", "dbins_column"})
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		insertKeys(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE dbins_reader", "CREATE ROLE dbins_writer", "CREATE ROLE dbins_column",
			"GRANT USAGE ON SCHEMA public,dbins_ext TO dbins_reader,dbins_writer,dbins_column",
			"GRANT SELECT ON insert_keys TO dbins_reader", "GRANT INSERT ON insert_keys TO dbins_writer", "GRANT SELECT(id) ON insert_keys TO dbins_column",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		held := insertHeld(t, p, c, insertCall)
		check := func(s fixture.Step) {
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		good := insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('9','a','O''Brien')", "insert_keys", "1", int64(1), "{1}", "{9}")
		denied := insertError(insertCall, "42501", "permission denied for table insert_keys", "insert_keys", "0", int64(0), "{}", "{}")
		run(fixture.Exec("SET SESSION AUTHORIZATION dbins_reader", 0))
		check(good)
		run(fixture.Select("SELECT has_table_privilege(current_user,'insert_keys','INSERT') AS can_insert", []string{"can_insert"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"dbins_writer", "dbins_column"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(denied)
			check(insertSelect(insertCall, nil, "insert_keys", "0", int64(0), "{}", nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT ON insert_keys TO dbins_writer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbins_writer", 0))
		check(good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON insert_keys FROM dbins_writer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbins_writer", 0))
		check(denied)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{
			"ALTER TABLE insert_keys ENABLE ROW LEVEL SECURITY",
			"CREATE POLICY insert_visible ON insert_keys FOR SELECT TO dbins_reader USING(id=2)",
			"SET SESSION AUTHORIZATION dbins_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		check(insertError(insertCall, "21000", "source row not found", "insert_keys", "1", int64(1), "{1}", "{9}"))
		check(insertSelect(insertCall, "INSERT INTO insert_keys(id,k,payload) VALUES('9',NULL,NULL)", "insert_keys", "1", int64(1), "{2}", "{9}"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE insert_keys DISABLE ROW LEVEL SECURITY", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION dbins_ext.dblink_build_sql_insert"+insertSignature+" FROM PUBLIC", 0))
		const child = "SELECT dbins_ext.dblink_build_sql_insert((1/(pg_backend_pid()*0))::text,'1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql"
		childHeld := insertHeld(t, p, c, child)
		run(fixture.Exec("CREATE TABLE insert_nullable(keys text[])", 0))
		run(fixture.Exec("INSERT INTO insert_nullable VALUES(NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON insert_nullable TO dbins_reader", 0))
		const dynamicNull = "SELECT dbins_ext.dblink_build_sql_insert('missing_insert_relation','1'::int2vector,1,keys,'{9}'::text[]) AS sql FROM insert_nullable"
		dynamicHeld := insertHeld(t, p, c, dynamicNull)
		for _, allowed := range []bool{false, true, false} {
			q := "REVOKE EXECUTE ON FUNCTION dbins_ext.dblink_build_sql_insert" + insertSignature + " FROM dbins_reader"
			if allowed {
				q = "GRANT EXECUTE ON FUNCTION dbins_ext.dblink_build_sql_insert" + insertSignature + " TO dbins_reader"
			}
			run(fixture.Exec(q, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION dbins_reader", 0))
			code, message := "42501", "permission denied for function dblink_build_sql_insert"
			if allowed {
				code, message = "22012", "division by zero"
			}
			s := insertError(child, code, message)
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, childHeld, b.Recorder, s)
			if allowed {
				check(good)
			} else {
				check(insertError(insertCall, code, message, "insert_keys", "1", int64(1), "{1}", "{9}"))
			}
			if allowed {
				insertBoth(t, p, b, c, insertSelect(dynamicNull, nil))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, insertSelect(dynamicNull, nil))
			} else {
				insertBoth(t, p, b, c, insertError(dynamicNull, code, message))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, insertError(dynamicNull, code, message))
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dbins_ext.dblink_build_sql_insert"+insertSignature+" TO dbins_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA dbins_ext FROM dbins_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbins_reader", 0))
		s := fixture.ImplicitError(insertCall, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema dbins_ext", Position: "8"}, "insert_keys", "1", int64(1), "{1}", "{9}")
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

func TestReferenceDblinkBuildSQLInsertIdentity(t *testing.T) {
	insertCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA dbins_left", "CREATE SCHEMA dbins_right",
			"CREATE TABLE dbins_left.insert_same(left_id integer)", "CREATE TABLE dbins_right.insert_same(right_id integer)",
			"CREATE TABLE insert_live(gone text,id integer,payload text)", "ALTER TABLE insert_live DROP COLUMN gone",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range []string{"INSERT INTO dbins_left.insert_same VALUES(1)", "INSERT INTO dbins_right.insert_same VALUES(1)", "INSERT INTO insert_live VALUES(1,'live')"} {
			run(fixture.Exec(q, 1))
		}
		held := insertHeld(t, p, c, insertCall)
		check := func(relation, want string) {
			s := insertSelect(insertCall, want, relation, "1", int64(1), "{1}", "{9}")
			insertBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET search_path=dbins_left,dbins_right,public", 0))
		check("insert_same", "INSERT INTO insert_same(left_id) VALUES('9')")
		check("dbins_left.insert_same", "INSERT INTO insert_same(left_id) VALUES('9')")
		check("dbins_right.insert_same", "INSERT INTO dbins_right.insert_same(right_id) VALUES('9')")
		run(fixture.Exec("SET search_path=dbins_right,dbins_left,public", 0))
		check("insert_same", "INSERT INTO insert_same(right_id) VALUES('9')")
		check("dbins_left.insert_same", "INSERT INTO dbins_left.insert_same(left_id) VALUES('9')")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("dbins_left.insert_same", `INSERT INTO "dbins_left"."insert_same"("left_id") VALUES('9')`)
		run(fixture.Exec("SET quote_all_identifiers=off", 0))
		run(fixture.Exec("SET search_path=public", 0))
		check("insert_live", "INSERT INTO insert_live(id,payload) VALUES('9','live')")
		run(fixture.Exec("CREATE VIEW insert_saved AS SELECT dbins_ext.dblink_build_sql_insert('public.insert_live','1'::int2vector,1,'{1}'::text[],'{9}'::text[]) AS sql", 0))
		run(fixture.Exec("ALTER TABLE insert_live RENAME COLUMN id TO current_id", 0))
		check("insert_live", "INSERT INTO insert_live(current_id,payload) VALUES('9','live')")
		insertBoth(t, p, b, c, insertSelect("SELECT sql FROM insert_saved", "INSERT INTO insert_live(current_id,payload) VALUES('9','live')"))
		run(fixture.Exec("ALTER TABLE insert_live RENAME TO insert_old", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, insertError(insertCall, "42P01", `relation "insert_live" does not exist`, "insert_live", "1", int64(1), "{1}", "{9}"))
		run(fixture.Exec("CREATE TABLE insert_live(replacement_id integer)", 0))
		run(fixture.Exec("INSERT INTO insert_live VALUES(1)", 1))
		check("insert_live", "INSERT INTO insert_live(replacement_id) VALUES('9')")
		insertBoth(t, p, b, c, insertSelect("SELECT sql FROM insert_saved", "INSERT INTO insert_live(replacement_id) VALUES('9')"))
		run(fixture.Exec("DROP TABLE insert_live", 0))
		run(fixture.Exec("CREATE TABLE insert_live(third_id integer)", 0))
		run(fixture.Exec("INSERT INTO insert_live VALUES(1)", 1))
		check("insert_live", "INSERT INTO insert_live(third_id) VALUES('9')")
		// Output functions use current settings; replacement TEXT is not
		// reformatted as a date, numeric, boolean or binary value.
		run(fixture.Exec("CREATE TABLE insert_output(id integer,n numeric,b boolean,d date,bytes bytea)", 0))
		run(fixture.Exec("INSERT INTO insert_output VALUES(1,12.50,true,DATE '2024-03-09',decode('5c27','hex'))", 1))
		for _, tc := range []struct{ setting, date, bytes string }{
			{"SET DateStyle='ISO, MDY'", "2024-03-09", `E'\\x5c27'`},
			{"SET DateStyle='SQL, DMY'", "09/03/2024", `E'\\x5c27'`},
		} {
			run(fixture.Exec(tc.setting, 0))
			insertBoth(t, p, b, c, insertSelect(insertCall, "INSERT INTO insert_output(id,n,b,d,bytes) VALUES('009','12.50','t','"+tc.date+"',"+tc.bytes+")", "insert_output", "1", int64(1), "{1}", "{009}"))
		}
		run(fixture.Exec("SET DateStyle='ISO, MDY'", 0))
		run(fixture.Exec("SET bytea_output='escape'", 0))
		insertBoth(t, p, b, c, insertSelect(insertCall, `INSERT INTO insert_output(id,n,b,d,bytes) VALUES('009','12.50','t','2024-03-09',E'\\\\''')`, "insert_output", "1", int64(1), "{1}", "{009}"))
	})
}
