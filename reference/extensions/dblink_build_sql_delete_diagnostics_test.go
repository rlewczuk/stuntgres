package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkBuildSQLDeleteDiagnostics(t *testing.T) {
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE delete_keys(id text,k2 text,payload text)", 0))
		held := deleteHeld(t, p, c, deleteCall)
		for _, tc := range []struct{ relation, vector string; count int64; array, code, message string }{
			{"delete_keys", "1", 0, "{}", "22023", "number of key attributes must be > 0"},
			{"delete_keys", "1", -1, "{a}", "22023", "number of key attributes must be > 0"},
			{"delete_keys", "", 1, "{a}", "22023", "number of key attributes must be > 0"},
			{"delete_keys", "", 1, "{}", "22023", "number of key attributes must be > 0"},
			{"delete_keys", "0", 1, "{a}", "22023", "invalid attribute number 0"},
			{"delete_keys", "-1", 1, "{a}", "22023", "invalid attribute number -1"},
			{"delete_keys", "4", 1, "{a}", "22023", "invalid attribute number 4"},
			{"delete_keys", "1 4", 2, "{}", "22023", "invalid attribute number 4"},
			{"delete_keys", "1 2", 2, "{a}", "2202E", "target key array length must match number of key attributes"},
			{"delete_keys", "1", 1, "{a,b}", "2202E", "target key array length must match number of key attributes"},
			{"delete_keys", "1", 1, "{}", "2202E", "target key array length must match number of key attributes"},
			{"missing_delete_relation", "0", 0, "{}", "42P01", `relation "missing_delete_relation" does not exist`},
			{"bad..name", "1", 1, "{a}", "42602", "invalid name syntax"},
		} {
			// PostgreSQL body errors expose no call-site Position/Context or
			// object fields beyond the exact severity, code and primary message.
			s := deleteError(deleteCall, tc.code, tc.message, tc.relation, tc.vector, tc.count, tc.array)
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, tc := range []struct{ vector, code, message string }{
			{"1 x", "22P02", `invalid input syntax for type smallint: "x"`},
			{"32768", "22003", `value "32768" is out of range for type smallint`},
		} {
			q := "SELECT dbdel_ext.dblink_build_sql_delete('delete_keys','" + tc.vector + "'::int2vector,1,'{a}'::text[]) AS sql"
			d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(strings.Index(q, "'"+tc.vector+"'") + 1)}
			s := fixture.ImplicitError(q, d)
			s.SelectRows = true
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
			// Bind conversion exposes portal-parameter context, not body context.
			d.Position, d.Context = "", "unnamed portal parameter $2 = '...'"
			s = fixture.ImplicitError(deleteCall, d, "delete_keys", tc.vector, int64(1), "{a}")
			s.SelectRows, s.ErrorStage = true, "execute"
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			// STRICT cannot hide malformed argument input conversion.
			s.Args[0] = nil
			deleteBoth(t, p, b, c, s)
		}
		// A constant malformed sibling is still converted during analysis even
		// when another argument is whole NULL.
		q := "SELECT dbdel_ext.dblink_build_sql_delete(NULL::text,'1 x'::int2vector,1,'{a}'::text[]) AS sql"
		d := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type smallint: "x"`, Position: fixture.Position(strings.Index(q, "'1 x'") + 1)}
		s := fixture.ImplicitError(q, d)
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		// Explicit savepoint recovery demonstrates that a builder failure does
		// not disguise a later poisoned-transaction error as another test case.
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("SAVEPOINT delete_error", 0))
			s := deleteError(deleteCall, "2202E", "target key array length must match number of key attributes", "delete_keys", "1", int64(1), "{}")
			s.Prepared = prepared
			run(s)
			run(fixture.Exec("ROLLBACK TO delete_error", 0))
			run(fixture.Exec("RELEASE delete_error", 0))
			run(deleteSelect(deleteCall, "DELETE FROM delete_keys WHERE id = 'a'", "delete_keys", "1", int64(1), "{a}"))
		}
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceDblinkBuildSQLDeletePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"dbdel_reader", "dbdel_deleter", "dbdel_column", "dbdel_installer"})
	deleteCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE ROLE dbdel_reader", "CREATE ROLE dbdel_deleter", "CREATE ROLE dbdel_column", "CREATE ROLE dbdel_installer",
			"GRANT USAGE ON SCHEMA public,dbdel_ext TO dbdel_reader,dbdel_deleter,dbdel_column,dbdel_installer",
			"CREATE TABLE delete_keys(id text,k2 text)", "GRANT SELECT ON delete_keys TO dbdel_reader",
			"GRANT DELETE ON delete_keys TO dbdel_deleter", "GRANT SELECT(id) ON delete_keys TO dbdel_column",
		} { run(fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		held := deleteHeld(t, p, c, deleteCall)
		good := deleteSelect(deleteCall, "DELETE FROM delete_keys WHERE id = 'a'", "delete_keys", "1", int64(1), "{a}")
		denied := deleteError(deleteCall, "42501", "permission denied for table delete_keys", "delete_keys", "0", int64(0), "{}")
		check := func(s fixture.Step) {
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_reader", 0))
		check(good) // SELECT-only is enough: builder must not demand DELETE.
		run(fixture.Select("SELECT has_table_privilege(current_user,'delete_keys','DELETE') AS can_delete", []string{"can_delete"}, []string{"BOOL"}, [][]any{{false}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"dbdel_deleter", "dbdel_column"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(denied) // relation SELECT precedes invalid key/count/length
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT SELECT ON delete_keys TO dbdel_deleter", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_deleter", 0))
		check(good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON delete_keys FROM dbdel_deleter", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_deleter", 0))
		check(denied)
		// Whole NULL bypasses table opening/SELECT, but not routine admission.
		check(deleteSelect(deleteCall, nil, "delete_keys", "1", int64(1), nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" FROM PUBLIC", 0))
		run(fixture.Exec("CREATE TABLE delete_nullable(keys text[])", 0))
		run(fixture.Exec("INSERT INTO delete_nullable VALUES(NULL)", 1))
		run(fixture.Exec("GRANT SELECT ON delete_nullable TO dbdel_reader", 0))
		q := "SELECT dbdel_ext.dblink_build_sql_delete((1/(pg_backend_pid()*0))::text,'1'::int2vector,1,'{a}'::text[]) AS sql"
		childHeld := deleteHeld(t, p, c, q)
		const dynamicNull = "SELECT dbdel_ext.dblink_build_sql_delete('missing_delete_relation','1'::int2vector,1,keys) AS sql FROM delete_nullable"
		const constantNull = "SELECT dbdel_ext.dblink_build_sql_delete('missing_delete_relation','1'::int2vector,1,NULL::text[]) AS sql"
		dynamicHeld := deleteHeld(t, p, c, dynamicNull)
		for _, allowed := range []bool{false, true, false} {
			grant := "REVOKE EXECUTE ON FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" FROM dbdel_reader"
			if allowed { grant = "GRANT EXECUTE ON FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" TO dbdel_reader" }
			run(fixture.Exec(grant, 0))
			// A fresh held parameterized statement distinguishes its first custom
			// execution from the multiply reused runtime/dynamic-NULL carrier.
			nullHeld := deleteHeld(t, p, c, deleteCall)
			run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_reader", 0))
			code, message := "42501", "permission denied for function dblink_build_sql_delete"
			if allowed { code, message = "22012", "division by zero" }
			// Ordinary EXECUTE admission precedes a demanded nonconstant child.
			s := deleteError(q, code, message)
			deleteBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, childHeld, b.Recorder, s)
			if allowed {
				check(good)
				deleteBoth(t, p, b, c, deleteSelect(dynamicNull, nil))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, deleteSelect(dynamicNull, nil))
			} else {
				check(deleteError(deleteCall, code, message, "delete_keys", "1", int64(1), "{a}"))
				deleteBoth(t, p, b, c, deleteError(dynamicNull, code, message))
				fixture.RunStep(t, p.Ctx, dynamicHeld, b.Recorder, deleteError(dynamicNull, code, message))
			}
			// Ordinary STRICT planning erases constant and first-execution bound
			// NULL calls before executor admission.
			deleteBoth(t, p, b, c, deleteSelect(constantNull, nil))
			null := deleteSelect(deleteCall, nil, "missing_delete_relation", "1", int64(1), nil)
			deleteBoth(t, p, b, c, null)
			fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, null)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION dbdel_ext.dblink_build_sql_delete"+deleteSignature+" TO dbdel_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA dbdel_ext FROM dbdel_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_reader", 0))
		s := fixture.ImplicitError(deleteCall, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema dbdel_ext", Position: "8"}, "delete_keys", "1", int64(1), "{a}")
		s.SelectRows = true
		run(s)
		s.Prepared, s.ErrorStage = true, "prepare"
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// The installation's stock superuser policy is observable independently
		// of selected-member EXECUTE, without invoking any adjacent callable.
		run(fixture.Exec("DROP EXTENSION dblink", 0))
		run(fixture.Exec("GRANT CREATE ON SCHEMA dbdel_ext TO dbdel_installer", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION dbdel_installer", 0))
		s = fixture.ImplicitError("CREATE EXTENSION dblink SCHEMA dbdel_ext", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "dblink"`, Hint: "Must be superuser to create this extension."})
		run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='dblink'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}
