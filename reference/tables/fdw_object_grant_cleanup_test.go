package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Cleanup must revoke received foreign ACLs, not merely delete foreign objects
// owned by the retiring role. Every successful probe drops its own DDL object.
func TestReferenceFDWObjectGrantCleanup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, grantOption := range []bool{false, true} {
			for _, family := range []struct {
				name, target, noun, catalog, column, key, use, drop string
			}{
				{"fdw_meta", "FOREIGN DATA WRAPPER fdw_meta", "foreign-data wrapper", "pg_foreign_data_wrapper", "fdwacl", "fdwname", "CREATE SERVER fdw_cleanup_probe FOREIGN DATA WRAPPER fdw_meta", "DROP SERVER fdw_cleanup_probe"},
				{"fdw_server", "FOREIGN SERVER fdw_server", "foreign server", "pg_foreign_server", "srvacl", "srvname", "CREATE FOREIGN TABLE fdw_cleanup_probe(id integer) SERVER fdw_server", "DROP FOREIGN TABLE fdw_cleanup_probe"},
			} {
				t.Run(fmt.Sprintf("%s/prepared=%t/grant_option=%t", family.name, prepared, grantOption), func(t *testing.T) {
					fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, _ *fdwSQLReader, baseRun func(fixture.Step)) {
						run := func(s fixture.Step) {
							s.Prepared = prepared
							baseRun(s)
						}
						defer func() {
							for _, q := range []string{
								"RESET SESSION AUTHORIZATION",
								"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_meta CASCADE",
								"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_cleanup_unrelated CASCADE",
							} {
								if _, err := c.ExecContext(p.Ctx, q); err != nil {
									t.Errorf("cleanup %s: %v", q, err)
								}
							}
						}()
						for _, role := range []string{"fdw_owner", "fdw_reader", "fdw_other"} {
							run(fixture.Exec("CREATE ROLE "+role, 0))
						}
						var bootstrap string
						if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrap); err != nil {
							t.Fatal(err)
						}
						// Namespace rights are intentionally independent of foreign U.
						run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO fdw_owner,fdw_reader,fdw_other", 0))
						run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta NO HANDLER NO VALIDATOR OPTIONS(tag 'wrapper')", 0))
						run(fixture.Exec("CREATE SERVER fdw_server TYPE 'cleanup' VERSION '1' FOREIGN DATA WRAPPER fdw_meta OPTIONS(tag 'server')", 0))
						run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_cleanup_unrelated NO HANDLER NO VALIDATOR OPTIONS(tag 'untouched')", 0))
						run(fixture.Exec("CREATE SERVER fdw_cleanup_unrelated_server FOREIGN DATA WRAPPER fdw_cleanup_unrelated OPTIONS(tag 'untouched-server')", 0))
						run(fixture.Exec("CREATE TABLE fdw_cleanup_before AS SELECT w.oid AS wrapper_oid,w.fdwowner AS wrapper_owner,w.fdwoptions::text AS wrapper_options,s.oid AS server_oid,s.srvowner AS server_owner,s.srvfdw AS parent_oid,s.srvtype,s.srvversion,s.srvoptions::text AS server_options FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid WHERE w.fdwname IN ('fdw_meta','fdw_cleanup_unrelated')", 2))
						unchanged := func() {
							run(fixture.Select("SELECT w.oid=x.wrapper_oid AS wrapper_identity,w.fdwowner=x.wrapper_owner AS wrapper_owner,w.fdwowner=r.oid AS bootstrap_owner,w.fdwhandler=0 AS no_handler,w.fdwvalidator=0 AS no_validator,w.fdwoptions::text=x.wrapper_options AS wrapper_options,s.oid=x.server_oid AS server_identity,s.srvowner=x.server_owner AS server_owner,s.srvowner=r.oid AS server_bootstrap,s.srvfdw=x.parent_oid AS parent_identity,s.srvtype IS NOT DISTINCT FROM x.srvtype AS server_type,s.srvversion IS NOT DISTINCT FROM x.srvversion AS server_version,s.srvoptions::text=x.server_options AS server_options FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid JOIN fdw_cleanup_before x ON x.wrapper_oid=w.oid AND x.server_oid=s.oid CROSS JOIN pg_roles r WHERE r.rolname='"+bootstrap+"' ORDER BY w.fdwname", []string{"wrapper_identity", "wrapper_owner", "bootstrap_owner", "no_handler", "no_validator", "wrapper_options", "server_identity", "server_owner", "server_bootstrap", "parent_identity", "server_type", "server_version", "server_options"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true, true, true, true, true, true}, {true, true, true, true, true, true, true, true, true, true, true, true, true}}))
						}
						for _, q := range []string{
							"SELECT fdwacl IS NULL AS absent FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta'",
							"SELECT srvacl IS NULL AS absent FROM pg_foreign_server WHERE srvname='fdw_server'",
						} {
							run(fixture.Select(q, []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
						}
						aclSQL := "SELECT unnest(" + family.column + ") AS acl FROM " + family.catalog + " WHERE " + family.key + "='" + family.name + "'"
						stmt, err := c.PrepareContext(p.Ctx, aclSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: aclSQL}
						raw := func(entries ...string) {
							t.Helper()
							rows := make([][]any, len(entries))
							for i, entry := range entries {
								rows[i] = []any{entry}
							}
							s := fixture.Select(aclSQL, []string{"acl"}, []string{"ACLITEM"}, rows)
							s.Bag = true
							run(s)
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						}
						probe := func(role string, allowed bool) {
							fixture.RoleGrantActor(run, role, func() {
								if allowed {
									run(fixture.Exec(family.use, 0))
									run(fixture.Exec(family.drop, 0))
								} else {
									fixture.RoleGrantPermissionDenied(run, family.use, family.noun, family.name)
								}
							})
						}
						raw()
						probe("fdw_reader", false) // Neither class defaults to PUBLIC U.
						run(fixture.Exec("GRANT USAGE ON "+family.target+" TO PUBLIC", 0))
						ownerEntry := bootstrap + "=U/" + bootstrap // Ordinary owner U, never U*.
						raw(ownerEntry, "=U/"+bootstrap)
						probe("fdw_reader", true)
						run(fixture.Exec("REVOKE USAGE ON "+family.target+" FROM PUBLIC", 0))
						raw(ownerEntry)
						probe("fdw_reader", false)
						// A separate untouched object's PUBLIC grant must survive cleanup.
						run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_cleanup_unrelated TO PUBLIC", 0))
						run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_cleanup_unrelated_server TO fdw_other", 0))
						suffix, readerEntry := "", "fdw_reader=U/"+bootstrap
						if grantOption {
							suffix, readerEntry = " WITH GRANT OPTION", "fdw_reader=U*/"+bootstrap
						}
						run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_reader"+suffix, 0))
						run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_other", 0))
						before := []string{ownerEntry, readerEntry, "fdw_other=U/" + bootstrap}
						if grantOption {
							fixture.RoleGrantActor(run, "fdw_reader", func() {
								run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_owner,fdw_other", 0))
							})
							before = append(before, "fdw_owner=U/fdw_reader", "fdw_other=U/fdw_reader")
						}
						raw(before...)
						probe("fdw_reader", true)
						probe("fdw_other", true)
						probe("fdw_owner", grantOption)
						// All cleanup roles must resolve before any earlier ACL can change.
						fixture.RoleGrantError(run, "DROP OWNED BY fdw_reader,fdw_cleanup_missing CASCADE", "42704", `role "fdw_cleanup_missing" does not exist`)
						raw(before...)
						probe("fdw_reader", true)
						probe("fdw_owner", grantOption)
						unchanged()
						run(fixture.Exec("DROP OWNED BY fdw_reader CASCADE", 0))
						raw(ownerEntry, "fdw_other=U/"+bootstrap)
						// DROP OWNED also removes public-schema ACLs. Restore only those
						// namespace rights, so the next 42501 names the foreign object.
						run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO fdw_reader", 0))
						probe("fdw_reader", false)
						probe("fdw_owner", false)
						probe("fdw_other", true)
						// Retire the newly restored namespace grants before DROP ROLE.
						run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM fdw_reader", 0))
						run(fixture.Exec("DROP ROLE fdw_reader", 0))
						run(fixture.Exec("CREATE ROLE fdw_reader", 0))
						run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO fdw_reader", 0))
						raw(ownerEntry, "fdw_other=U/"+bootstrap)
						probe("fdw_reader", false)
						fixture.RoleGrantActor(run, "fdw_reader", func() {
							fixture.RoleGrantPermissionDenied(run, "GRANT USAGE ON "+family.target+" TO fdw_owner", family.noun, family.name)
							// This new identity can still use the genuinely preserved PUBLIC
							// grant on an unrelated object, not retired reader authority.
							run(fixture.Exec("CREATE SERVER fdw_cleanup_probe FOREIGN DATA WRAPPER fdw_cleanup_unrelated", 0))
							run(fixture.Exec("DROP SERVER fdw_cleanup_probe", 0))
						})
						fixture.RoleGrantActor(run, "fdw_other", func() {
							run(fixture.Exec("CREATE FOREIGN TABLE fdw_cleanup_probe(id integer) SERVER fdw_cleanup_unrelated_server", 0))
							run(fixture.Exec("DROP FOREIGN TABLE fdw_cleanup_probe", 0))
						})
						for _, s := range []fixture.Step{
							fixture.Select("SELECT unnest(fdwacl) AS acl FROM pg_foreign_data_wrapper WHERE fdwname='fdw_cleanup_unrelated'", []string{"acl"}, []string{"ACLITEM"}, [][]any{{ownerEntry}, {"=U/" + bootstrap}}),
							fixture.Select("SELECT unnest(srvacl) AS acl FROM pg_foreign_server WHERE srvname='fdw_cleanup_unrelated_server'", []string{"acl"}, []string{"ACLITEM"}, [][]any{{ownerEntry}, {"fdw_other=U/" + bootstrap}}),
						} {
							s.Bag = true
							run(s)
						}
						unchanged()
					})
				})
			}
		}
	}
}
