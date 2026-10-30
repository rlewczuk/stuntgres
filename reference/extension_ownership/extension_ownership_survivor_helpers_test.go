package reference_test

import (
	"stuntgres/reference/fixture"
)

func ownershipSurvivorLinks(run func(fixture.Step), signature string, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	run(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='`+signature+`'::regprocedure`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
	run(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
	run(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='`+signature+`'::regprocedure`, []string{"rolname", "allowed", "explicit_acl", "public_allowed"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"eo_survivor_owner", true, true, false}}))
}

func ownershipSurvivorNormalRestriction(run func(fixture.Step), signature, view string) {
	s := fixture.OwnershipError("DROP FUNCTION "+signature, "2BP01", "cannot drop function "+signature+" because other objects depend on it")
	s.Diagnostic.Detail = "view " + view + " depends on function " + signature
	s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	run(s)
}
