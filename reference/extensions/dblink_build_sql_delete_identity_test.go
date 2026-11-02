package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLDeleteIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbdel_path_reader"})
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE SCHEMA dbdel_left", "CREATE SCHEMA dbdel_right", "CREATE SCHEMA dbdel_moved",
			"CREATE TABLE dbdel_left.delete_same(left_id text)", "CREATE TABLE dbdel_right.delete_same(right_id text)",
			`CREATE TABLE "123"(id text)`, "CREATE TABLE delete_live(id text,payload text)",
		} { run(fixture.Exec(q, 0)) }
		held := deleteHeld(t, p, c, deleteCall)
		check := func(relation, expected string) {
			s := deleteSelect(deleteCall, expected, relation, "1", int64(1), "{a}")
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET search_path=dbdel_left,dbdel_right,public", 0))
		check("delete_same", "DELETE FROM delete_same WHERE left_id = 'a'")
		check("dbdel_left.delete_same", "DELETE FROM delete_same WHERE left_id = 'a'")
		check("dbdel_right.delete_same", "DELETE FROM dbdel_right.delete_same WHERE right_id = 'a'")
		run(fixture.Exec("SET search_path=dbdel_right,dbdel_left,public", 0))
		check("delete_same", "DELETE FROM delete_same WHERE right_id = 'a'")
		check("dbdel_left.delete_same", "DELETE FROM dbdel_left.delete_same WHERE left_id = 'a'")
		run(fixture.Exec("SET quote_all_identifiers=on", 0))
		check("dbdel_left.delete_same", `DELETE FROM "dbdel_left"."delete_same" WHERE "left_id" = 'a'`)
		run(fixture.Exec("SET quote_all_identifiers=off", 0))
		run(fixture.Exec("SET search_path=public", 0))
		check("dbdel_left.delete_same", "DELETE FROM dbdel_left.delete_same WHERE left_id = 'a'")
		check("123", `DELETE FROM "123" WHERE id = 'a'`)
		check(`public."123"`, `DELETE FROM "123" WHERE id = 'a'`)
		for _, q := range []string{
			"CREATE ROLE dbdel_path_reader", "GRANT USAGE ON SCHEMA public,dbdel_ext,dbdel_right TO dbdel_path_reader",
			"GRANT SELECT ON dbdel_left.delete_same,dbdel_right.delete_same TO dbdel_path_reader",
			"SET search_path=dbdel_left,dbdel_right,public", "SET SESSION AUTHORIZATION dbdel_path_reader",
		} { run(fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		// An inaccessible schema in search_path is ignored, even with table ACL.
		check("delete_same", "DELETE FROM delete_same WHERE right_id = 'a'")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("CREATE VIEW delete_saved AS SELECT dbdel_ext.dblink_build_sql_delete('public.delete_live','1'::int2vector,1,'{a}'::text[]) AS sql", 0))
		saved := deleteSelect("SELECT sql FROM delete_saved", "DELETE FROM delete_live WHERE id = 'a'")
		deleteBoth(t, p, b, c, saved)
		check("delete_live", "DELETE FROM delete_live WHERE id = 'a'")
		run(fixture.Exec("ALTER TABLE delete_live RENAME COLUMN id TO current_id", 0))
		saved.Rows = [][]any{{"DELETE FROM delete_live WHERE current_id = 'a'"}}
		deleteBoth(t, p, b, c, saved)
		check("delete_live", "DELETE FROM delete_live WHERE current_id = 'a'")
		run(fixture.Exec("ALTER TABLE delete_live RENAME TO delete_old", 0))
		deleteBoth(t, p, b, c, deleteError(deleteCall, "42P01", `relation "delete_live" does not exist`, "delete_live", "1", int64(1), "{a}"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, deleteError(deleteCall, "42P01", `relation "delete_live" does not exist`, "delete_live", "1", int64(1), "{a}"))
		deleteBoth(t, p, b, c, deleteError("SELECT sql FROM delete_saved", "42P01", `relation "public.delete_live" does not exist`))
		run(fixture.Exec("CREATE TABLE delete_live(replacement_id text)", 0))
		saved.Rows = [][]any{{"DELETE FROM delete_live WHERE replacement_id = 'a'"}}
		deleteBoth(t, p, b, c, saved)
		check("delete_live", "DELETE FROM delete_live WHERE replacement_id = 'a'")
		run(fixture.Exec("DROP TABLE delete_live", 0)) // text literal did not create a relation dependency
		run(fixture.Exec("CREATE TABLE delete_live(third_id text)", 0))
		saved.Rows = [][]any{{"DELETE FROM delete_live WHERE third_id = 'a'"}}
		deleteBoth(t, p, b, c, saved)
		check("delete_live", "DELETE FROM delete_live WHERE third_id = 'a'")
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbdel_ext' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'").Scan(&original); err != nil { t.Fatal(err) }
		for _, q := range []string{
			"ALTER FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" RENAME TO delete_builder",
			"ALTER FUNCTION dbdel_ext.delete_builder"+deleteSignature+" SET SCHEMA dbdel_moved",
		} { run(fixture.Exec(q, 0)) }
		deleteBoth(t, p, b, c, saved)
		deleteBoth(t, p, b, c, deleteMember("delete_builder", "dbdel_moved"))
		q := "SELECT dbdel_moved.delete_builder($1::text,$2::int2vector,$3::integer,$4::text[]) AS sql"
		deleteBoth(t, p, b, c, deleteSelect(q, "DELETE FROM delete_live WHERE third_id = 'a'", "delete_live", "1", int64(1), "{a}"))
		run(fixture.Exec("CREATE FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" RETURNS text LANGUAGE SQL AS 'SELECT ''ordinary shadow''::text'", 0))
		run(fixture.Exec("SET search_path=dbdel_ext,public", 0))
		deleteBoth(t, p, b, c, deleteSelect("SELECT dblink_build_sql_delete('missing','0'::int2vector,0,'{}'::text[]) AS sql", "ordinary shadow"))
		deleteBoth(t, p, b, c, saved)
		run(fixture.Exec("DROP FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature, 0))
		run(fixture.Exec("ALTER EXTENSION dblink DROP FUNCTION dbdel_moved.delete_builder"+deleteSignature, 0))
		deleteBoth(t, p, b, c, deleteSelect(q, "DELETE FROM delete_live WHERE third_id = 'a'", "delete_live", "1", int64(1), "{a}"))
		run(fixture.Exec("ALTER EXTENSION dblink ADD FUNCTION dbdel_moved.delete_builder"+deleteSignature, 0))
		run(fixture.Exec("ALTER FUNCTION dbdel_moved.delete_builder"+deleteSignature+" SET SCHEMA dbdel_ext", 0))
		run(fixture.Exec("ALTER FUNCTION dbdel_ext.delete_builder"+deleteSignature+" RENAME TO dblink_build_sql_delete", 0))
		run(fixture.Exec("ALTER EXTENSION dblink SET SCHEMA dbdel_moved", 0))
		deleteBoth(t, p, b, c, saved)
		deleteBoth(t, p, b, c, deleteMember("dblink_build_sql_delete", "dbdel_moved"))
		deleteBoth(t, p, b, c, fixture.Select("SELECT p.oid=$1::oid AS retained FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbdel_moved' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'", []string{"retained"}, []string{"BOOL"}, [][]any{{true}}, original))
		run(fixture.Exec("DROP EXTENSION dblink CASCADE", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='delete_saved' AND relnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE EXTENSION dblink SCHEMA dbdel_ext", 0))
		deleteBoth(t, p, b, c, fixture.Select("SELECT p.oid<>$1::oid AND p.oid<>0 AS fresh FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbdel_ext' AND p.proname='dblink_build_sql_delete' AND p.proargtypes::text='25 22 23 1009'", []string{"fresh"}, []string{"BOOL"}, [][]any{{true}}, original))
		deleteBoth(t, p, b, c, deleteSelect(deleteCall, "DELETE FROM delete_live WHERE third_id = 'a'", "delete_live", "1", int64(1), "{a}"))
	})
}
