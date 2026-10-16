package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// The owning SQL/MED fixture supplies real metadata DDL on both backends.
// No handler, validator, remote connection or scan implementation is needed.
func TestReferenceFDWObjectGrants(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "fdw_grant_private")
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, baseRun func(fixture.Step)) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					baseRun(s)
				}
				defer func() {
					for _, q := range []string{
						"RESET SESSION AUTHORIZATION",
						"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_grant_self CASCADE",
						"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_meta CASCADE",
						"DROP SCHEMA IF EXISTS fdw_grant_private CASCADE",
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
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO fdw_owner,fdw_reader,fdw_other", 0))
				run(fixture.Exec("CREATE SCHEMA fdw_grant_private", 0))
				run(fixture.Exec("CREATE TABLE fdw_grant_private.local_data(id integer)", 0))
				run(fixture.Exec("INSERT INTO fdw_grant_private.local_data VALUES(17)", 1))
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta NO HANDLER NO VALIDATOR", 0))
				acl := func(catalog, column, key, name string, entries ...string) {
					t.Helper()
					rows := make([][]any, len(entries))
					for i, entry := range entries {
						rows[i] = []any{entry}
					}
					s := fixture.Select("SELECT unnest("+column+") AS acl FROM "+catalog+" WHERE "+key+"='"+name+"'", []string{"acl"}, []string{"ACLITEM"}, rows)
					s.Bag = true
					run(s)
				}
				nullACL := func(catalog, column, key, name string, absent bool) {
					run(fixture.Select("SELECT "+column+" IS NULL AS absent FROM "+catalog+" WHERE "+key+"='"+name+"'", []string{"absent"}, []string{"BOOL"}, [][]any{{absent}}))
				}
				wrapperACL := func(entries ...string) { acl("pg_foreign_data_wrapper", "fdwacl", "fdwname", "fdw_meta", entries...) }
				serverACL := func(entries ...string) { acl("pg_foreign_server", "srvacl", "srvname", "fdw_server", entries...) }
				ownerEntry := bootstrap + "=U/" + bootstrap
				nullACL("pg_foreign_data_wrapper", "fdwacl", "fdwname", "fdw_meta", true)
				wrapperACL()
				fixture.RoleGrantActor(run, "fdw_owner", func() {
					fixture.RoleGrantPermissionDenied(run, "CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", "foreign-data wrapper", "fdw_meta")
				})
				run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_meta TO fdw_owner", 0))
				nullACL("pg_foreign_data_wrapper", "fdwacl", "fdwname", "fdw_meta", false)
				wrapperACL(ownerEntry, "fdw_owner=U/"+bootstrap)
				fixture.RoleGrantActor(run, "fdw_owner", func() {
					run(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta", 0))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_owner_before(id integer) SERVER fdw_server", 0))
				})
				nullACL("pg_foreign_server", "srvacl", "srvname", "fdw_server", true)
				serverACL()
				fixture.RoleGrantActor(run, "fdw_owner", func() {
					run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_reader", 0))
					// First modification must retain the owner's ordinary default U.
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_owner_after(id integer) SERVER fdw_server", 0))
				})
				nullACL("pg_foreign_server", "srvacl", "srvname", "fdw_server", false)
				serverACL("fdw_owner=U/fdw_owner", "fdw_reader=U/fdw_owner")
				fixture.RoleGrantActor(run, "fdw_owner", func() {
					run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER fdw_server FROM fdw_owner", 0))
					serverACL("fdw_reader=U/fdw_owner")
					fixture.RoleGrantPermissionDenied(run, "CREATE FOREIGN TABLE fdw_owner_denied(id integer) SERVER fdw_server", "foreign server", "fdw_server")
					// Owner's implicit GO survives, but is not displayed as U*.
					run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER fdw_server TO fdw_owner", 0))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_owner_restored(id integer) SERVER fdw_server", 0))
				})
				serverACL("fdw_owner=U/fdw_owner", "fdw_reader=U/fdw_owner")

				// A default-equivalent self grant still materializes an explicit ACL.
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_grant_self NO HANDLER NO VALIDATOR", 0))
				run(fixture.Exec("CREATE SERVER fdw_self_server FOREIGN DATA WRAPPER fdw_grant_self", 0))
				for _, target := range []struct{ sql, catalog, column, key, name string }{
					{"FOREIGN DATA WRAPPER fdw_grant_self", "pg_foreign_data_wrapper", "fdwacl", "fdwname", "fdw_grant_self"},
					{"FOREIGN SERVER fdw_self_server", "pg_foreign_server", "srvacl", "srvname", "fdw_self_server"},
				} {
					nullACL(target.catalog, target.column, target.key, target.name, true)
					run(fixture.Exec("GRANT USAGE ON "+target.sql+" TO CURRENT_USER", 0))
					nullACL(target.catalog, target.column, target.key, target.name, false)
					acl(target.catalog, target.column, target.key, target.name, ownerEntry)
				}

				// Schema authority and relation SELECT remain separate from server U.
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT id FROM fdw_grant_private.local_data", "schema", "fdw_grant_private", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA fdw_grant_private TO fdw_reader", 0))
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					denied := fixture.ImplicitError("CREATE FOREIGN TABLE fdw_grant_private.schema_denied(id integer) SERVER fdw_server", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema fdw_grant_private", Position: fixture.Position(22)})
					denied.ErrorStage = "execute"
					run(denied)
					fixture.RoleGrantPermissionDenied(run, "SELECT id FROM fdw_grant_private.local_data", "table", "local_data")
				})
				run(fixture.Exec("GRANT CREATE ON SCHEMA fdw_grant_private TO fdw_reader", 0))
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
					run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
					run(fixture.Exec("CREATE FOREIGN TABLE fdw_grant_private.allowed(id integer) SERVER fdw_server", 0))
					fixture.RoleGrantPermissionDenied(run, "SELECT id FROM fdw_grant_private.local_data", "table", "local_data")
				})
				run(fixture.Exec("GRANT SELECT ON fdw_grant_private.local_data TO fdw_reader", 0))
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					run(fixture.Select("SELECT id FROM fdw_grant_private.local_data", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
				})
				run(fixture.Exec("CREATE TABLE fdw_grant_identity AS SELECT 'wrapper'::text AS kind,oid AS object_oid FROM pg_foreign_data_wrapper WHERE fdwname='fdw_meta' UNION ALL SELECT 'server'::text,oid FROM pg_foreign_server WHERE srvname='fdw_server'", 2))
				run(fixture.Select("SELECT n.nspowner=r.oid AS schema_owner,c.relowner=r.oid AS table_owner FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid CROSS JOIN pg_roles r WHERE n.nspname='fdw_grant_private' AND c.relname='local_data' AND r.rolname='"+bootstrap+"'", []string{"schema_owner", "table_owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))

				for _, family := range []struct {
					target, noun, name, owner, consumer, drop string
					baseline                                  []string
					raw                                       func(...string)
				}{
					{"FOREIGN DATA WRAPPER fdw_meta", "foreign-data wrapper", "fdw_meta", bootstrap, "CREATE SERVER fdw_grant_probe FOREIGN DATA WRAPPER fdw_meta", "DROP SERVER fdw_grant_probe", []string{ownerEntry, "fdw_owner=U/" + bootstrap}, wrapperACL},
					{"FOREIGN SERVER fdw_server", "foreign server", "fdw_server", "fdw_owner", "CREATE FOREIGN TABLE fdw_grant_probe(id integer) SERVER fdw_server", "DROP FOREIGN TABLE fdw_grant_probe", []string{"fdw_owner=U/fdw_owner"}, serverACL},
				} {
					asOwner := func(body func()) { fixture.RoleGrantActor(run, family.owner, body) }
					raw := func(extra ...string) {
						entries := append(append([]string{}, family.baseline...), extra...)
						family.raw(entries...)
					}
					asOwner(func() {
						run(fixture.Exec("REVOKE ALL ON "+family.target+" FROM fdw_reader,fdw_other,PUBLIC CASCADE", 0))
					})
					raw()
					fixture.RoleGrantActor(run, "fdw_other", func() {
						fixture.RoleGrantPermissionDenied(run, family.consumer, family.noun, family.name)
						if family.name == "fdw_server" {
							fixture.RoleGrantPermissionDenied(run, "CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server", "foreign server", "fdw_server")
						}
						fixture.RoleGrantPermissionDenied(run, "GRANT USAGE ON "+family.target+" TO fdw_reader", family.noun, family.name)
					})
					asOwner(func() {
						fixture.RoleGrantError(run, "GRANT ALL ON "+family.target+" TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles")
						fixture.RoleGrantError(run, "GRANT USAGE ON "+family.target+" TO fdw_reader GRANTED BY fdw_other", "0A000", "grantor must be current user")
						run(fixture.Exec("GRANT ALL PRIVILEGES ON "+family.target+" TO fdw_reader WITH GRANT OPTION GRANTED BY CURRENT_USER", 0))
					})
					raw("fdw_reader=U*/" + family.owner)
					fixture.RoleGrantActor(run, "fdw_reader", func() {
						run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_other GRANTED BY CURRENT_ROLE", 0))
					})
					raw("fdw_reader=U*/"+family.owner, "fdw_other=U/fdw_reader")
					asOwner(func() {
						restrict := fixture.ImplicitError("REVOKE GRANT OPTION FOR USAGE ON "+family.target+" FROM fdw_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
						restrict.ErrorStage = "execute"
						run(restrict)
					})
					raw("fdw_reader=U*/"+family.owner, "fdw_other=U/fdw_reader")
					fixture.RoleGrantActor(run, "fdw_other", func() {
						run(fixture.Exec(family.consumer, 0))
						run(fixture.Exec(family.drop, 0))
					})
					asOwner(func() {
						run(fixture.Exec("REVOKE GRANT OPTION FOR USAGE ON "+family.target+" FROM fdw_reader CASCADE", 0))
					})
					raw("fdw_reader=U/" + family.owner)
					fixture.RoleGrantActor(run, "fdw_other", func() { fixture.RoleGrantPermissionDenied(run, family.consumer, family.noun, family.name) })
					fixture.RoleGrantActor(run, "fdw_reader", func() {
						run(fixture.Exec(family.consumer, 0))
						run(fixture.Exec(family.drop, 0))
					})
					// Two independent grant origins: CASCADE removes only the
					// reader's origin, leaving the owner's ordinary grant intact.
					asOwner(func() {
						run(fixture.Exec("GRANT ALL ON "+family.target+" TO fdw_reader WITH GRANT OPTION", 0))
						run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_other", 0))
					})
					fixture.RoleGrantActor(run, "fdw_reader", func() { run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_other", 0)) })
					raw("fdw_reader=U*/"+family.owner, "fdw_other=U/"+family.owner, "fdw_other=U/fdw_reader")
					asOwner(func() {
						run(fixture.Exec("REVOKE GRANT OPTION FOR ALL ON "+family.target+" FROM fdw_reader CASCADE", 0))
					})
					raw("fdw_reader=U/"+family.owner, "fdw_other=U/"+family.owner)
					fixture.RoleGrantActor(run, "fdw_other", func() {
						run(fixture.Exec(family.consumer, 0))
						run(fixture.Exec(family.drop, 0))
					})

					var notices []fixture.Diagnostic
					_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "SET SESSION AUTHORIZATION fdw_reader")
					fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT ALL ON "+family.target+" TO fdw_other", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "` + family.name + `"`})
					fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "REVOKE USAGE ON "+family.target+" FROM fdw_other", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `no privileges could be revoked for "` + family.name + `"`})
					fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "RESET SESSION AUTHORIZATION")
					raw("fdw_reader=U/"+family.owner, "fdw_other=U/"+family.owner)

					// Late lookup failure cannot publish the valid first target.
					missingTarget := "FOREIGN SERVER fdw_server,fdw_grant_missing"
					missingMessage := `server "fdw_grant_missing" does not exist`
					if family.name == "fdw_meta" {
						missingTarget = "FOREIGN DATA WRAPPER fdw_meta,fdw_grant_missing"
						missingMessage = `foreign-data wrapper "fdw_grant_missing" does not exist`
					}
					asOwner(func() {
						run(fixture.Exec("REVOKE ALL ON "+family.target+" FROM fdw_other", 0))
						fixture.RoleGrantError(run, "GRANT ALL ON "+missingTarget+" TO fdw_other", "42704", missingMessage)
					})
					raw("fdw_reader=U/" + family.owner)
					fixture.RoleGrantActor(run, "fdw_other", func() { fixture.RoleGrantPermissionDenied(run, family.consumer, family.noun, family.name) })
					asOwner(func() { run(fixture.Exec("GRANT USAGE ON "+family.target+" TO fdw_other", 0)) })
					stmt, err := c.PrepareContext(p.Ctx, family.consumer)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: family.consumer}
					useHeld := func(allowed bool) {
						fixture.RoleGrantActor(run, "fdw_other", func() {
							s := fixture.Exec(family.consumer, 0)
							if !allowed {
								s = fixture.ImplicitError(family.consumer, fixture.Diagnostic{Code: "42501", Message: "permission denied for " + family.noun + " " + family.name})
								s.ErrorStage = "execute"
							}
							fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							if allowed {
								run(fixture.Exec(family.drop, 0))
							}
						})
					}
					useHeld(true)
					asOwner(func() {
						fixture.RoleGrantError(run, "REVOKE ALL ON "+missingTarget+" FROM fdw_other", "42704", missingMessage)
					})
					useHeld(true)
					raw("fdw_reader=U/"+family.owner, "fdw_other=U/"+family.owner)
					asOwner(func() { run(fixture.Exec("REVOKE ALL ON "+family.target+" FROM fdw_other", 0)) })
					useHeld(false)
					asOwner(func() { run(fixture.Exec("GRANT ALL PRIVILEGES ON "+family.target+" TO PUBLIC", 0)) })
					raw("fdw_reader=U/"+family.owner, "=U/"+family.owner)
					useHeld(true)
					if family.name == "fdw_server" {
						fixture.RoleGrantActor(run, "fdw_other", func() {
							run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
							run(fixture.Exec("DROP USER MAPPING FOR CURRENT_USER SERVER fdw_server", 0))
						})
					}
					asOwner(func() { run(fixture.Exec("REVOKE ALL ON "+family.target+" FROM PUBLIC", 0)) })
					useHeld(false)
					raw("fdw_reader=U/" + family.owner)
				}
				run(fixture.Select("SELECT b.kind,w.oid=b.object_oid AS same_object,w.fdwhandler=0 AS no_handler,w.fdwvalidator=0 AS no_validator FROM pg_foreign_data_wrapper w CROSS JOIN fdw_grant_identity b WHERE b.kind='wrapper' AND w.fdwname='fdw_meta'", []string{"kind", "same_object", "no_handler", "no_validator"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"wrapper", true, true, true}}))
				run(fixture.Select("SELECT s.oid=b.object_oid AS same_object,s.srvowner=r.oid AS same_owner FROM pg_foreign_server s CROSS JOIN fdw_grant_identity b CROSS JOIN pg_roles r WHERE b.kind='server' AND s.srvname='fdw_server' AND r.rolname='fdw_owner'", []string{"same_object", "same_owner"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
			})
		})
	}
}
