package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These are grants on real registered procedure identities, not a standalone SQL
// implementation of a C handler. PostgreSQL's internal FDW calls do not check X.
func TestReferenceFDWRoutineGrants(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, baseRun func(fixture.Step)) {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					baseRun(s)
				}
				defer func() {
					for _, q := range []string{
						"RESET SESSION AUTHORIZATION", "RESET ROLE",
						"DROP FOREIGN DATA WRAPPER IF EXISTS fdw_routine_bound CASCADE",
						"GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC",
					} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}()
				fdwLive(t, p, b, reader, run)
				handler, validator, scanOnly := "fdw_sql_handler", "fdw_sql_validator", "fdw_scan_only_handler"
				if b.PG {
					handler, validator, scanOnly = "postgres_fdw_handler", "postgres_fdw_validator", "file_fdw_handler"
					run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS file_fdw", 0))
				}
				for _, role := range []string{"fdw_reader", "fdw_other", "fdw_owner"} {
					run(fixture.Exec("CREATE ROLE "+role, 0))
				}
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_reader,fdw_other,fdw_owner", 0))
				targets := "public." + handler + "(),public." + validator + "(text[],oid)"
				type member struct {
					name, signature, inputs string
					arity, result           int64
				}
				members := []member{{handler, "", "", 0, 3115}, {validator, "text[],oid", "1009 26", 2, 2278}, {scanOnly, "", "", 0, 3115}}
				seen := map[int64]bool{}
				var owner string
				for _, m := range members {
					var oid int64
					var memberOwner string
					if err := c.QueryRowContext(p.Ctx, "SELECT p.oid::bigint,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='public' AND p.proname=$1", m.name).Scan(&oid, &memberOwner); err != nil {
						t.Fatal(m.name, err)
					}
					if oid <= 0 || seen[oid] || memberOwner == "" || owner != "" && owner != memberOwner {
						t.Fatal("invalid registered routine identity/owner relationship", m.name, oid, memberOwner)
					}
					seen[oid], owner = true, memberOwner
					run(fixture.Select("SELECT p.oid::regprocedure::text AS identity,p.pronargs,p.prorettype::bigint AS result,p.proisstrict,p.provolatile,p.proparallel,n.nspname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1",
						[]string{"identity", "pronargs", "result", "proisstrict", "provolatile", "proparallel", "nspname"}, []string{"TEXT", "INT2", "INT8", "BOOL", "CHAR", "CHAR", "NAME"},
						[][]any{{m.name + "(" + m.signature + ")", m.arity, m.result, true, "v", "u", "public"}}, oid))
					full := fixture.Select("SELECT p.proargtypes::text AS inputs,p.prokind,p.prolang::bigint AS language,p.proretset,p.prosecdef,p.provariadic::bigint AS variadic,p.pronargdefaults,p.proargnames::text AS names,p.proallargtypes::text AS alltypes,p.proargmodes::text AS modes,p.proacl IS NULL AS absent FROM pg_proc p WHERE p.oid=$1",
						[]string{"inputs", "prokind", "language", "proretset", "prosecdef", "variadic", "pronargdefaults", "names", "alltypes", "modes", "absent"}, []string{"TEXT", "CHAR", "INT8", "BOOL", "BOOL", "INT8", "INT2", "TEXT", "TEXT", "TEXT", "BOOL"},
						[][]any{{m.inputs, "f", int64(13), false, false, int64(0), int64(0), nil, nil, nil, true}}, oid)
					if b.PG {
						run(full)
					}
				}

				// Raw catalog ACLs distinguish NULL, explicit defaults and {}. Bag
				// assertions specify origins without assuming the ACL item's order.
				heldACLs := make(map[string]*sql.Stmt)
				defer func() {
					for _, stmt := range heldACLs {
						stmt.Close()
					}
				}()
				raw := func(name string, absent bool, entries ...string) {
					call := "pg_get_acl(1255,'public." + name + "'::regprocedure,0)"
					run(fixture.Select("SELECT "+call+" IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{absent}}))
					rows := make([][]any, len(entries))
					for i, entry := range entries {
						rows[i] = []any{entry}
					}
					s := fixture.Select("SELECT unnest("+call+") AS acl", []string{"acl"}, []string{"ACLITEM"}, rows)
					s.Bag = true
					run(s)
					stmt := heldACLs[name]
					if stmt == nil {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, s.Query)
						if err != nil {
							t.Fatal(err)
						}
						heldACLs[name] = stmt
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
				}
				h := handler + "()"
				v := validator + "(text[],oid)"
				raw(h, true)
				raw(v, true)
				raw(scanOnly+"()", true)
				query := "SELECT has_function_privilege('fdw_reader','public." + h + "','EXECUTE') AS handler,has_function_privilege('fdw_reader','public." + v + "','EXECUTE') AS validator,has_function_privilege('fdw_other','public." + h + "','EXECUTE') AS delegated,has_function_privilege('fdw_reader','public." + h + "','EXECUTE WITH GRANT OPTION') AS grantable"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				privileges := func(handlerX, validatorX, otherX, goX bool) {
					s := fixture.Select(query, []string{"handler", "validator", "delegated", "grantable"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{handlerX, validatorX, otherX, goX}})
					run(s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				privileges(true, true, true, false)
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets+" TO PUBLIC", 0))
				raw(h, false, "=X/"+owner, owner+"=X/"+owner)
				raw(v, false, "=X/"+owner, owner+"=X/"+owner)
				privileges(true, true, true, false)
				run(fixture.Exec("REVOKE ALL ON ROUTINE "+targets+" FROM PUBLIC,"+fdwQuote(owner), 0))
				raw(h, false)
				raw(v, false)
				privileges(false, false, false, false)
				// Ordinary owner rights are revocable; implicit owner options recover
				// use even from an explicit-empty ACL.
				run(fixture.Exec("GRANT ALL PRIVILEGES ON FUNCTION "+targets+" TO "+fdwQuote(owner)+" GRANTED BY CURRENT_USER", 0))
				raw(h, false, owner+"=X/"+owner)
				raw(v, false, owner+"=X/"+owner)

				// Internal FDW dispatch still works with PUBLIC X absent. Wrapper
				// creation is genuinely superuser-only; the server creator is not.
				run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_routine_bound HANDLER "+handler+" VALIDATOR "+validator, 0))
				run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_routine_bound TO fdw_reader", 0))
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					run(fixture.Exec("CREATE SERVER fdw_routine_server FOREIGN DATA WRAPPER fdw_routine_bound", 0))
					if b.PG {
						// A real postgres_fdw option exercises its internal validator;
						// the native SQL reader intentionally has different options.
						run(fixture.Exec("ALTER SERVER fdw_routine_server OPTIONS(dbname 'postgres')", 0))
					}
				})
				run(fixture.Select("SELECT w.fdwhandler='public."+h+"'::regprocedure::oid AS handler,w.fdwvalidator='public."+v+"'::regprocedure::oid AS validator,s.srvowner=r.oid AS creator FROM pg_foreign_data_wrapper w JOIN pg_foreign_server s ON s.srvfdw=w.oid JOIN pg_roles r ON r.rolname='fdw_reader' WHERE w.fdwname='fdw_routine_bound' AND s.srvname='fdw_routine_server'", []string{"handler", "validator", "creator"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
				run(fixture.Exec("GRANT SELECT ON fdw_data TO fdw_reader", 0))
				if b.PG {
					run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS(user 'stuntgres',password 'stuntgres',password_required 'false')", 0))
				} else {
					run(fixture.Exec("CREATE USER MAPPING FOR fdw_reader SERVER fdw_live", 0))
				}
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
					for _, m := range members[:2] {
						fixture.RoleGrantPermissionDenied(run, "GRANT EXECUTE ON FUNCTION public."+m.name+"("+m.signature+") TO fdw_other", "function", m.name)
					}
				})

				// Quiet Prepare, execute-phase warning: PUBLIC/use-only rights
				// never become a grant-option source.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets+" TO fdw_reader", 0))
				raw(h, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
				raw(v, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
				privileges(true, true, false, false)
				var notices []fixture.Diagnostic
				_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "SET SESSION AUTHORIZATION fdw_reader")
				for _, m := range members[:2] {
					fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT EXECUTE ON FUNCTION public."+m.name+"("+m.signature+") TO fdw_other", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "` + m.name + `"`})
				}
				fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "RESET SESSION AUTHORIZATION")
				privileges(true, true, false, false)
				raw(h, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
				raw(v, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
				run(fixture.Exec("GRANT ALL PRIVILEGES ON ROUTINE "+targets+" TO fdw_reader WITH GRANT OPTION GRANTED BY CURRENT_ROLE", 0))
				fixture.RoleGrantActor(run, "fdw_reader", func() {
					run(fixture.Exec("GRANT ALL ON FUNCTION "+targets+" TO fdw_other GRANTED BY CURRENT_USER", 0))
					fixture.RoleGrantError(run, "GRANT EXECUTE ON ROUTINE "+targets+" TO fdw_owner GRANTED BY fdw_other", "0A000", "grantor must be current user")
				})
				privileges(true, true, true, true)
				raw(h, false, owner+"=X/"+owner, "fdw_reader=X*/"+owner, "fdw_other=X/fdw_reader")
				raw(v, false, owner+"=X/"+owner, "fdw_reader=X*/"+owner, "fdw_other=X/fdw_reader")
				// Keep an independent owner-origin grant to other while removing
				// only reader-origin dependents, for both full signatures.
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+targets+" TO fdw_other", 0))
				restrict := fixture.ImplicitError("REVOKE GRANT OPTION FOR EXECUTE ON ROUTINE "+targets+" FROM fdw_reader RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
				restrict.ErrorStage = "execute"
				run(restrict)
				privileges(true, true, true, true)
				run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON ROUTINE "+targets+" FROM fdw_reader CASCADE", 0))
				privileges(true, true, true, false)
				raw(h, false, owner+"=X/"+owner, "fdw_reader=X/"+owner, "fdw_other=X/"+owner)
				raw(v, false, owner+"=X/"+owner, "fdw_reader=X/"+owner, "fdw_other=X/"+owner)
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+targets+" FROM fdw_other", 0))
				privileges(true, true, false, false)

				// A valid first target or grantee must not publish before a late
				// malformed signature, unknown role or syntax suffix fails.
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT EXECUTE ON FUNCTION public." + h + ",public." + validator + "(integer) TO fdw_other", "42883", "function public." + validator + "(integer) does not exist"},
					{"REVOKE EXECUTE ON FUNCTION public." + h + ",public." + validator + "(integer) FROM fdw_reader", "42883", "function public." + validator + "(integer) does not exist"},
					{"GRANT EXECUTE ON FUNCTION " + targets + " TO fdw_other,fdw_missing", "42704", `role "fdw_missing" does not exist`},
					{"REVOKE EXECUTE ON FUNCTION " + targets + " FROM fdw_reader,fdw_missing", "42704", `role "fdw_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION " + targets + " TO fdw_other GRANTED BY fdw_missing", "42704", `role "fdw_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION " + targets + " TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT USAGE ON FUNCTION " + targets + " TO fdw_other", "0LP01", "invalid privilege type USAGE for function"},
				} {
					fixture.RoleGrantError(run, tc.q, tc.code, tc.message)
					privileges(true, true, false, false)
					raw(h, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
					raw(v, false, owner+"=X/"+owner, "fdw_reader=X/"+owner)
				}
				malformed := "GRANT EXECUTE ON FUNCTION " + targets + " TO fdw_other unexpected"
				fixture.RoleGrantSyntax(run, malformed, "unexpected", prepared)
				privileges(true, true, false, false)

				// Schema expansion selects actual members, not a guessed complete
				// public bag: PG extensions have extra members absent natively.
				run(fixture.Exec("CREATE PROCEDURE public.fdw_routine_proc() LANGUAGE SQL AS 'SELECT 1'", 0))
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
				run(fixture.Exec("REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA public FROM PUBLIC,fdw_reader,fdw_other CASCADE", 0))
				schemaRights := func(want bool) {
					for _, m := range members {
						run(fixture.Select("SELECT has_function_privilege('fdw_reader','public."+m.name+"("+m.signature+")','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{want}}))
						if !b.PG {
							if want {
								raw(m.name+"("+m.signature+")",false,owner+"=X/"+owner,"fdw_reader=X/"+owner)
							} else {
								raw(m.name+"("+m.signature+")",false,owner+"=X/"+owner)
							}
						}
					}
					run(fixture.Select("SELECT has_function_privilege('fdw_reader','pg_catalog.abs(integer)','EXECUTE') AS outside", []string{"outside"}, []string{"BOOL"}, [][]any{{false}}))
					if !b.PG {
						run(fixture.Select("SELECT unnest(pg_get_acl(1255,'pg_catalog.abs(integer)'::regprocedure,0)) AS acl",[]string{"acl"},[]string{"ACLITEM"},[][]any{{owner+"=X/"+owner}}))
					}
				}
				procedureRight := func(want bool) {
					run(fixture.Select("SELECT has_function_privilege('fdw_reader','public.fdw_routine_proc()','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{want}}))
				}
				schemaRights(false)
				procedureRight(false)
				run(fixture.Exec("GRANT EXECUTE ON ALL PROCEDURES IN SCHEMA public TO fdw_reader", 0))
				schemaRights(false)
				procedureRight(true)
				run(fixture.Exec("REVOKE EXECUTE ON ALL PROCEDURES IN SCHEMA public FROM fdw_reader", 0))
				procedureRight(false)
				run(fixture.Exec("GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO fdw_reader", 0))
				schemaRights(true)
				procedureRight(false)
				run(fixture.Exec("REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM fdw_reader", 0))
				schemaRights(false)
				run(fixture.Exec("GRANT EXECUTE ON ALL ROUTINES IN SCHEMA public TO fdw_reader", 0))
				schemaRights(true)
				procedureRight(true)
				run(fixture.Exec("REVOKE EXECUTE ON ALL ROUTINES IN SCHEMA public FROM fdw_reader", 0))
				schemaRights(false)
				procedureRight(false)
				privileges(false, false, false, false)
				run(fixture.Exec("GRANT ALL ON ROUTINE "+targets+" TO PUBLIC", 0))
				raw(h, false, owner+"=X/"+owner, "=X/"+owner)
				raw(v, false, owner+"=X/"+owner, "=X/"+owner)
				privileges(true, true, true, false)
				run(fixture.Exec("REVOKE ALL PRIVILEGES ON FUNCTION "+targets+" FROM PUBLIC", 0))
				raw(h, false, owner+"=X/"+owner)
				raw(v, false, owner+"=X/"+owner)
				privileges(false, false, false, false)
			})
		})
	}
}
