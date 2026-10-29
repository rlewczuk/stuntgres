package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ownershipProviderMetadataSurvivors(t *testing.T) {
	for _, provider := range []string{"pgrowlocks", "intagg_enum", "intagg_aggregate"} {
		t.Run(provider, func(t *testing.T) {
			states := []string{"attached", "detached", "foreign", "absent"}
			if provider == "intagg_aggregate" {
				states = states[:3]
			}
			for _, state := range states {
				t.Run(state, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := ownershipRun(t, p, b, c)
						ownershipSurvivorRoles(run)
						origin, name, input, kind := "pgrowlocks", "pgrowlocks", "text", "FUNCTION"
						if provider == "intagg_enum" {
							origin, name, input = "intagg", "int_array_enum", "integer[]"
						}
						if provider == "intagg_aggregate" {
							origin, name, input, kind = "intagg", "int_array_aggregate", "integer", "AGGREGATE"
						}
						run(fixture.Exec(`CREATE EXTENSION `+origin+` SCHEMA eo_old`, 0))
						signature := "eo_old." + name + "(" + input + ")"
						if kind == "AGGREGATE" {
							run(fixture.Exec(`ALTER AGGREGATE `+signature+` OWNER TO eo_survivor_owner`, 0))
							run(fixture.Exec(`REVOKE ALL ON FUNCTION `+signature+` FROM PUBLIC`, 0))
							run(fixture.Exec(`GRANT EXECUTE ON FUNCTION `+signature+` TO eo_survivor_acl`, 0))
						} else {
							ownershipSurvivorACL(run, signature)
							run(fixture.Exec(`ALTER FUNCTION `+signature+` PARALLEL RESTRICTED COST 7.25 ROWS 13`, 0))
						}
						id := ownershipID(t, p, c, `SELECT '`+signature+`'::regprocedure::bigint`)
						initialQuery := ownershipSurvivorDeclaration(signature)
						initial := ownershipSnapshot(t, p, c, initialQuery)
						// The aggregate's private helper identities retain their genuine source
						// coordinates. They are not independently mutable native SQL functions.
						graphQuery := `SELECT a.aggtransfn::text,a.aggfinalfn::text,a.aggtranstype::bigint,a.aggfnoid::oid<>a.aggtransfn::oid AS distinct_state,a.aggfnoid::oid<>a.aggfinalfn::oid AS distinct_final FROM pg_aggregate a WHERE a.aggfnoid::oid='` + signature + `'::regprocedure::oid`
						var graph [][]any
						if kind == "AGGREGATE" {
							run(fixture.Select(graphQuery, []string{"aggtransfn", "aggfinalfn", "aggtranstype", "distinct_state", "distinct_final"}, []string{"TEXT", "TEXT", "INT8", "BOOL", "BOOL"}, [][]any{{"eo_old.int_agg_state", "eo_old.int_agg_final_array", int64(2281), true, true}}))
							graph = ownershipSnapshot(t, p, c, graphQuery)
						}
						if state != "attached" {
							run(fixture.Exec(`ALTER EXTENSION `+origin+` DROP `+kind+` `+signature, 0))
							ownershipUnchanged(t, p, c, initialQuery, initial)
						}
						run(fixture.Exec(`ALTER `+kind+` `+signature+` RENAME TO eo_provider`, 0))
						signature = "eo_old.eo_provider(" + input + ")"
						run(fixture.Exec(`ALTER `+kind+` `+signature+` SET SCHEMA eo_new`, 0))
						signature = "eo_new.eo_provider(" + input + ")"
						if state == "foreign" {
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD `+kind+` `+signature, 0))
						}
						// Intagg aggregate helpers remain normally required. Only the independent
						// enumerator retires its origin; there is no helper ALTER or CASCADE trick.
						retire := state == "absent" || state == "foreign" && kind != "AGGREGATE"
						if retire {
							run(fixture.Exec(`DROP EXTENSION `+origin, 0))
						}
						if got := ownershipID(t, p, c, `SELECT '`+signature+`'::regprocedure::bigint`); got != id {
							t.Fatal("provider mutation changed identity", got, id)
						}
						run(fixture.Exec(`CREATE TABLE eo_new.provider_input(id integer,k text,v integer)`, 0))
						// Exact deterministic corpus from binary_json_arrays/intagg_test.go.
						run(fixture.Exec(`INSERT INTO eo_new.provider_input VALUES(1,'a',2),(2,'a',NULL),(3,'a',2),(4,'b',-2147483648),(5,'b',2147483647)`, 5))
						run(fixture.Exec(`GRANT SELECT ON eo_new.provider_input TO eo_survivor_acl`, 0))
						query := `SELECT count(*) AS n FROM eo_new.eo_provider('eo_new.provider_input'::text)`
						if provider == "intagg_enum" {
							query = `SELECT v FROM eo_new.eo_provider('{2,2,NULL}'::integer[]) WITH ORDINALITY AS u(v,n) ORDER BY n`
						}
						if kind == "AGGREGATE" {
							query = `SELECT eo_new.eo_provider(v ORDER BY id) AS v FROM eo_new.provider_input`
						}
						run(fixture.Exec(`CREATE VIEW eo_new.provider_saved AS `+query, 0))
						declarationQuery := ownershipSurvivorDeclaration(signature)
						current := ownershipSnapshot(t, p, c, declarationQuery)
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
							ownershipUnchanged(t, p, conn, declarationQuery, current)
							member := ""
							if state == "attached" {
								member = origin
							}
							if state == "foreign" {
								member = "uuid-ossp"
							}
							r(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='`+signature+`'::regprocedure`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
							r(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
							r(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='`+signature+`'::regprocedure`, []string{"rolname", "allowed", "public_allowed"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"eo_survivor_owner", true, false}}))
							present := int64(1)
							if retire {
								present = 0
							}
							r(ownershipCount(`SELECT count(*) FROM pg_extension WHERE extname='`+origin+`'`, present))
							source, args, result, strict, sets, prokind, parallel, cost, rows := "pgrowlocks", "25", int64(2249), true, true, "f", "r", 7.25, float64(13)
							var binary any = "$libdir/pgrowlocks"
							if provider == "intagg_enum" {
								source, args, result, binary = "array_unnest", "1007", 23, nil
							}
							if kind == "AGGREGATE" {
								source, args, result, strict, sets, prokind, parallel, cost, rows, binary = "aggregate_dummy", "23", 1007, false, false, "a", "s", 1, 0, nil
							}
							r(fixture.Select(`SELECT p.prosrc,p.probin,p.proargtypes::text,p.prorettype::bigint,p.proisstrict,p.proretset,p.prokind,p.proparallel,p.procost,p.prorows FROM pg_proc p WHERE p.oid='`+signature+`'::regprocedure`, []string{"prosrc", "probin", "proargtypes", "prorettype", "proisstrict", "proretset", "prokind", "proparallel", "procost", "prorows"}, []string{"TEXT", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "CHAR", "CHAR", "FLOAT4", "FLOAT4"}, [][]any{{source, binary, args, result, strict, sets, prokind, parallel, cost, rows}}))
							if provider != "pgrowlocks" {
								r(fixture.Select(`SELECT proallargtypes::text,proargmodes::text,proargnames::text FROM pg_proc WHERE oid='`+signature+`'::regprocedure`, []string{"proallargtypes", "proargmodes", "proargnames"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}))
							}
							var s fixture.Step
							if provider == "pgrowlocks" {
								r(fixture.Select(`SELECT proallargtypes::text,proargmodes::text,proargnames::text FROM pg_proc WHERE oid='`+signature+`'::regprocedure`, []string{"proallargtypes", "proargmodes", "proargnames"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"{25,27,28,16,1011,1009,1007}", "{i,o,o,o,o,o,o}", "{relname,locked_row,locker,multi,xids,modes,pids}"}}))
								s = fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
							} else if provider == "intagg_enum" {
								s = fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}})
							} else {
								s = fixture.ArrayReferenceSelect(query, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{2,NULL,2,-2147483648,2147483647}"}}, b.PG)
								currentGraph := `SELECT a.aggtransfn::text,a.aggfinalfn::text,a.aggtranstype::bigint,a.aggfnoid::oid<>a.aggtransfn::oid AS distinct_state,a.aggfnoid::oid<>a.aggfinalfn::oid AS distinct_final FROM pg_aggregate a WHERE a.aggfnoid::oid='` + signature + `'::regprocedure::oid`
								ownershipUnchanged(t, p, conn, currentGraph, graph)
								r(ownershipCount(`SELECT count(*) FROM pg_depend d JOIN pg_aggregate a ON a.aggfnoid::oid=d.objid WHERE d.classid='pg_proc'::regclass AND d.objid='`+signature+`'::regprocedure AND d.refclassid='pg_proc'::regclass AND d.deptype='n' AND (d.refobjid=a.aggtransfn::oid OR d.refobjid=a.aggfinalfn::oid)`, 2))
							}
							r(s)
							r(fixture.Prepared(s))
							r(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
							r(s)
							r(fixture.Prepared(s))
							r(fixture.Exec(`RESET ROLE`, 0))
							stored := s
							stored.Query = `SELECT * FROM eo_new.provider_saved`
							r(stored)
							r(fixture.Prepared(stored))
							if kind != "AGGREGATE" {
								null := fixture.Select(`SELECT count(*) AS n FROM eo_new.eo_provider(NULL::`+input+`)`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
								r(null)
								r(fixture.Prepared(null))
								if member == "" {
									ownershipSurvivorNormalRestriction(r, signature, "eo_new.provider_saved")
								}
								if member != "" {
									ownershipSurvivorMemberRestriction(r, signature, member)
								}
							}
						}
						ownershipSurvivorControls(t, p, b, c, provider+"_"+state, []string{signature}, check)
					})
				})
			}
		})
	}
}
