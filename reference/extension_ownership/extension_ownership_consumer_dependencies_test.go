package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These are explicit SQL function occurrences, including SRFs, rather than
// implementation carriers of operator syntax. Their normal edges are separate
// from the analysis lifetime manifest and from mutable extension membership.
func TestReferenceExtensionOwnershipConsumerDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, origin, signature, query, typ string
		value                               any
	}{
		{"array", "hstore", "hstore_to_array(eo_new.hstore)", "SELECT eo_new.hstore_to_array('a=>1'::eo_new.hstore)::text AS v", "TEXT", "{a,1}"},
		{"json", "hstore", "hstore_to_json(eo_new.hstore)", "SELECT eo_new.hstore_to_json('a=>1'::eo_new.hstore) IS NOT NULL AS v", "BOOL", true},
		{"construct", "hstore", "hstore(text,text)", "SELECT eo_new.hstore('a','1')::text AS v", "TEXT", `"a"=>"1"`},
		{"record", "hstore", "hstore(record)", "SELECT eo_new.hstore(NULL::eo_old.dep_pair)::text AS v", "TEXT", `"i"=>NULL`},
		{"rows", "hstore", "skeys(eo_new.hstore)", "SELECT eo_new.skeys('a=>1'::eo_new.hstore) AS v", "TEXT", "a"},
		{"string", "citext", "replace(eo_new.citext,eo_new.citext,eo_new.citext)", "SELECT eo_new.replace('ab'::eo_new.citext,''::eo_new.citext,'X'::eo_new.citext) AS v", "TEXT", "XaXbX"},
		{"regexp", "citext", "regexp_match(eo_new.citext,eo_new.citext)", "SELECT eo_new.regexp_match('AbA'::eo_new.citext,'a'::eo_new.citext)::text AS v", "TEXT", "{A}"},
		{"regexp_set", "citext", "regexp_matches(eo_new.citext,eo_new.citext)", "SELECT eo_new.regexp_matches('AbA'::eo_new.citext,'a'::eo_new.citext)::text AS v", "TEXT", "{A}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, foreign := range []bool{false, true} {
				state := "detached"
				if foreign {
					state = "foreign"
				}
				t.Run(state, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"eo_survivor_owner", "eo_survivor_acl"})
					ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						run := ownershipRun(t, p, b, c)
						ownershipSurvivorRoles(run)
						run(fixture.Exec("CREATE EXTENSION "+tc.origin+" SCHEMA eo_new", 0))
						run(fixture.Exec("CREATE TYPE eo_old.dep_pair AS (i integer)", 0))
						signature := "eo_new." + tc.signature
						ownershipSurvivorACL(run, signature)
						step := fixture.Select(tc.query, []string{"v"}, []string{tc.typ}, [][]any{{tc.value}})
						run(step)
						run(fixture.Prepared(step))
						run(fixture.Exec("CREATE VIEW eo_old.dep_consumer AS "+tc.query, 0))
						run(fixture.Exec("GRANT SELECT ON eo_old.dep_consumer TO eo_survivor_acl", 0))
						held := fixture.CrosstabHeld(t, p, c, "SELECT * FROM eo_old.dep_consumer")
						defer held.Statement.Close()
						run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_rewrite'::regclass AND objid IN (SELECT oid FROM pg_rewrite WHERE ev_class='eo_old.dep_consumer'::regclass) AND refclassid='pg_proc'::regclass AND refobjid='`+signature+`'::regprocedure AND deptype='n'`, 1))
						run(fixture.Exec("ALTER EXTENSION "+tc.origin+" DROP FUNCTION "+signature, 0))
						if foreign {
							run(fixture.Exec(`ALTER EXTENSION "uuid-ossp" ADD FUNCTION `+signature, 0))
						}
						stored := step
						stored.Query = "SELECT * FROM eo_old.dep_consumer"
						fixture.RunStep(t, p.Ctx, held, b.Recorder, stored)
						if err := held.Statement.Close(); err != nil {
							t.Fatal(err)
						}
						check := func(conn *sql.Conn, recorder *fixture.Recorder) {
							r := ownershipRunWithRecorder(t, p, conn, recorder)
							for _, s := range []fixture.Step{step, fixture.Prepared(step), stored, fixture.Prepared(stored)} {
								r(s)
							}
							r(fixture.Exec("SET ROLE eo_survivor_acl", 0))
							r(stored)
							r(fixture.Exec("RESET ROLE", 0))
							ownershipConsumerLinks(r, signature, foreign)
						}
						ownershipSurvivorControls(t, p, b, c, "consumer_"+tc.name+"_"+state, []string{signature}, check)
					})
				})
			}
		})
	}
}

// Both normal edges describe the same unchanged source or restored destination.
// Project them together, retaining the separate membership and owner/ACL values.
func ownershipConsumerLinks(run func(fixture.Step), signature string, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	run(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of,r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='`+signature+`'::regprocedure`, []string{"member_of", "rolname", "allowed", "explicit_acl", "public_allowed"}, []string{"TEXT", "NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{member, "eo_survivor_owner", true, true, false}}))
	run(fixture.Select(`SELECT count(*) FILTER (WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n') AS function_namespace,count(*) FILTER (WHERE classid='pg_rewrite'::regclass AND objid IN (SELECT oid FROM pg_rewrite WHERE ev_class='eo_old.dep_consumer'::regclass) AND refclassid='pg_proc'::regclass AND refobjid='`+signature+`'::regprocedure AND deptype='n') AS view_function FROM pg_depend`, []string{"function_namespace", "view_function"}, []string{"INT8", "INT8"}, [][]any{{int64(1), int64(1)}}))
}
