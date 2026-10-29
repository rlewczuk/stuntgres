package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Numeric identities are checked within one catalog only. This logical snapshot
// includes the entire pg_proc declaration and current owner/ACL; regtype strings
// deliberately resolve destination identities instead of comparing source OIDs.
func ownershipSurvivorDeclaration(signature string) string {
	return `SELECT p.proname,n.nspname,r.rolname,p.proacl::text,l.lanname,p.prokind,p.proargtypes::oid[]::regtype[]::text,p.proallargtypes::regtype[]::text,p.proargmodes::text,p.proargnames::text,p.prorettype::regtype::text,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc,p.probin,p.proconfig::text,p.prosecdef,p.proleakproof,p.pronargs,p.pronargdefaults,p.proargdefaults::text,p.provariadic::regtype::text,p.prosupport::text,p.protrftypes::regtype[]::text,p.prosqlbody::text FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='` + signature + `'::regprocedure`
}

func ownershipSurvivorLinks(run func(fixture.Step), signature string, foreign bool) {
	member := ""
	if foreign {
		member = "uuid-ossp"
	}
	run(fixture.Select(`SELECT COALESCE(e.extname::text,'') AS member_of FROM pg_proc p LEFT JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.refclassid='pg_extension'::regclass AND d.deptype='e' LEFT JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid='`+signature+`'::regprocedure`, []string{"member_of"}, []string{"TEXT"}, [][]any{{member}}))
	run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_namespace'::regclass AND refobjid='eo_new'::regnamespace AND deptype='n'`, 1))
	run(fixture.Select(`SELECT r.rolname,has_function_privilege('eo_survivor_acl',p.oid,'EXECUTE') AS allowed,p.proacl IS NOT NULL AS explicit_acl,has_function_privilege('public',p.oid,'EXECUTE') AS public_allowed FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='`+signature+`'::regprocedure`, []string{"rolname", "allowed", "explicit_acl", "public_allowed"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"eo_survivor_owner", true, true, false}}))
}

func ownershipSurvivorRoles(run func(fixture.Step)) {
	for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`, `CREATE SCHEMA eo_old`, `CREATE SCHEMA eo_new`, `CREATE SCHEMA eo_other`, `GRANT USAGE ON SCHEMA eo_old,eo_new TO eo_survivor_acl`, `CREATE EXTENSION "uuid-ossp" SCHEMA eo_other`} {
		run(fixture.Exec(q, 0))
	}
}

func ownershipSurvivorACL(run func(fixture.Step), signature string) {
	for _, q := range []string{`ALTER FUNCTION ` + signature + ` OWNER TO eo_survivor_owner`, `REVOKE ALL ON FUNCTION ` + signature + ` FROM PUBLIC`, `GRANT EXECUTE ON FUNCTION ` + signature + ` TO eo_survivor_acl`} {
		run(fixture.Exec(q, 0))
	}
}

func ownershipSurvivorControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, signatures []string, check func(*sql.Conn, *fixture.Recorder)) {
	t.Helper()
	check(c, b.Recorder)
	queries := make([]string, len(signatures))
	before := make([][][]any, len(signatures))
	for i, signature := range signatures {
		queries[i] = ownershipSurvivorDeclaration(signature)
		before[i] = ownershipSnapshot(t, p, c, queries[i])
		if len(before[i]) != 1 {
			t.Fatal("missing genuine survivor declaration", signature)
		}
	}
	if b.PG {
		return
	}
	saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
	// Controls use raw pools; assertions use that destination's recording adapter.
	restore := func(control, recorded *sql.DB, recorder *fixture.Recorder) {
		if err := stuntgres.Reset(p.Ctx, control); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		conn, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		check(conn, recorder)
		for i, q := range queries {
			ownershipUnchanged(t, p, conn, q, before[i])
		}
	}
	restore(p.Native, b.DB, b.Recorder)
	connector, err := stuntgres.NewDriver().OpenConnector("eo_survivor_" + name)
	if err != nil {
		t.Fatal(err)
	}
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	other := sql.OpenDB(connector)
	defer other.Close()
	for _, q := range []string{`CREATE ROLE eo_survivor_owner SUPERUSER`, `CREATE ROLE eo_survivor_acl`} {
		if _, err := other.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	restore(other, recorded, recorder)
}

// Exercise mutation failures against the actual released object, then prove
// rollback restores its exact reciprocal membership and logical declaration.
func ownershipSurvivorMutation(t *testing.T, p *fixture.Pair, c *sql.Conn, run func(fixture.Step), origin, signature string) {
	declaration := ownershipSurvivorDeclaration(signature)
	before := ownershipSnapshot(t, p, c, declaration)
	for _, receiver := range []string{origin, `"uuid-ossp"`} {
		run(ownershipError("ALTER EXTENSION "+receiver+" ADD FUNCTION "+signature, "55000", "function "+signature+` is already a member of extension "`+origin+`"`))
	}
	ownershipSurvivorNonmember(run, `"uuid-ossp"`, signature)
	run(fixture.Exec(`SET ROLE eo_survivor_acl`, 0))
	routine := strings.Split(strings.Split(signature, "(")[0], ".")
	run(ownershipError("ALTER FUNCTION "+signature+" RENAME TO eo_forbidden", "42501", "must be owner of function "+routine[len(routine)-1]))
	run(fixture.Exec(`RESET ROLE`, 0))
	run(fixture.Exec(`BEGIN`, 0))
	run(fixture.Exec(`SAVEPOINT survivor_membership`, 0))
	run(fixture.Exec("ALTER EXTENSION "+origin+" DROP FUNCTION "+signature, 0))
	run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_extension'::regclass AND deptype='e'`, 0))
	run(fixture.Exec(`ROLLBACK TO survivor_membership`, 0))
	run(fixture.Exec(`COMMIT`, 0))
	run(ownershipCount(`SELECT count(*) FROM pg_depend WHERE classid='pg_proc'::regclass AND objid='`+signature+`'::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='`+origin+`') AND deptype='e'`, 1))
	ownershipUnchanged(t, p, c, declaration, before)
}

func ownershipSurvivorNonmember(run func(fixture.Step), extension, signature string) {
	run(ownershipError("ALTER EXTENSION "+extension+" DROP FUNCTION "+signature, "55000", "function "+signature+` is not a member of extension "`+strings.Trim(extension, `"`)+`"`))
}

func ownershipSurvivorMemberRestriction(run func(fixture.Step), signature, extension string) {
	for _, suffix := range []string{"", " CASCADE"} {
		s := ownershipError("DROP FUNCTION "+signature+suffix, "2BP01", "cannot drop function "+signature+" because extension "+extension+" requires it")
		s.Diagnostic.Hint = "You can drop extension " + extension + " instead."
		run(s)
	}
}

func ownershipSurvivorNormalRestriction(run func(fixture.Step), signature, view string) {
	s := ownershipError("DROP FUNCTION "+signature, "2BP01", "cannot drop function "+signature+" because other objects depend on it")
	s.Diagnostic.Detail = "view " + view + " depends on function " + signature
	s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	run(s)
}
