package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func backupInitial(pg bool) fixture.Step {
	return fixture.Select("SELECT objoid::bigint AS oid,classoid::bigint AS class,objsubid,privtype::text AS kind,initprivs::text AS acl FROM pg_init_privs WHERE classoid=1255 AND objoid IN (2172,2739) ORDER BY objoid", []string{"oid", "class", "objsubid", "kind", "acl"}, []string{"INT8", "INT8", "INT4", "TEXT", "TEXT"}, [][]any{{int64(2172), int64(1255), int64(0), "i", recoveryControlOwnerACL(pg)}, {int64(2739), int64(1255), int64(0), "i", recoveryControlOwnerACL(pg)}})
}
func TestReferenceBackupControlPrivileges(t *testing.T) {
	const reader = "backup_acl_reader"
	const group = "backup_acl_group"
	const repl = "backup_acl_replication"
	fixture.RoleReferenceCleanup(t, []string{reader, group, repl})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, role := range []string{reader, group} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("CREATE ROLE "+repl+" REPLICATION", 0))
		defer backupRestore(t, p, b, c, reader, group, repl)()
		run(backupInitial(b.PG))
		q := "SELECT pg_backup_start($1::text,true)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		check := func(start, stop bool) {
			for i, id := range backupIdentities {
				allowed := start
				if i == 1 {
					allowed = stop
				}
				run(fixture.Select("SELECT has_function_privilege(current_user,'"+id+"','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{allowed}}))
			}
			if !start {
				s := recoveryControlDenied(q, "pg_backup_start")
				s.Args = []any{strings.Repeat("é", 513)}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			if !stop {
				backupRoutes(t, p, b, c, recoveryControlDenied("SELECT pg_backup_stop(NULL)", "pg_backup_stop"))
			}
		}
		for _, role := range []string{reader, repl} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(false, false)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_start(text,boolean) TO "+reader+" WITH GRANT OPTION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		check(true, false)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_stop(boolean) TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		check(true, true)
		backupPair(t, p, b, c, held, q, "granted_backup", "SELECT * FROM pg_backup_stop(false)", false, "granted_backup")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE ALL ON FUNCTION pg_backup_start(text,boolean) FROM "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		check(false, true)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_start(text,boolean) TO "+group, 0))
		run(fixture.Exec("GRANT "+group+" TO "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		check(true, true)
		backupPair(t, p, b, c, held, q, "inherited_backup", "SELECT * FROM pg_backup_stop(false)", false, "inherited_backup")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE "+group+" FROM "+reader, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+reader, 0))
		check(false, true)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_start(text,boolean) TO PUBLIC", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_backup_stop(boolean) TO PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+repl, 0))
		check(true, true)
		backupPair(t, p, b, c, held, q, "public_backup", "SELECT * FROM pg_backup_stop(false)", false, "public_backup")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, id := range backupIdentities {
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+id+" FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+repl, 0))
		check(false, false)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(backupInitial(b.PG))
	})
}
