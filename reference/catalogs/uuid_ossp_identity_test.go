package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func uuidOsspInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, owner, memberOwner string) {
	t.Helper()
	var ext, ns, proc, nargs, ret int64
	var version, gotOwner, gotMember, vol, parallel, signature string
	var relocatable, strict bool
	if err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extnamespace,extversion,extrelocatable,rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE extname='uuid-ossp'").Scan(&ext, &ns, &version, &relocatable, &gotOwner); err != nil || ext <= 0 || ns <= 0 || version != "1.1" || !relocatable || gotOwner != owner {
		t.Fatalf("extension identity %d/%d/%s/%v/%s: %v", ext, ns, version, relocatable, gotOwner, err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT p.oid,pronargs,prorettype,provolatile,proisstrict,proparallel,rolname,p.oid::regprocedure::text FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE pronamespace=$1 AND proname='uuid_generate_v4'", ns).Scan(&proc, &nargs, &ret, &vol, &strict, &parallel, &gotMember, &signature); err != nil || proc <= 0 || nargs != 0 || ret != 2950 || vol != "v" || !strict || parallel != "s" || gotMember != memberOwner || signature != "uuid_ossp_part.uuid_generate_v4()" {
		t.Fatalf("procedure %d nargs=%d ret=%d vol=%s strict=%v parallel=%s owner=%s signature=%s: %v", proc, nargs, ret, vol, strict, parallel, gotMember, signature, err)
	}
	var count int64
	if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", proc, ext).Scan(&count); err != nil || count != 1 {
		t.Fatal("membership", count, err)
	}
}

func TestReferenceUUIDOsspIdentity(t *testing.T) {
	uuidOsspCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		var bootstrapOwner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&bootstrapOwner); err != nil {
			t.Fatal(err)
		}
		uuidOsspFailure(t, p, c, "SELECT uuid_generate_v4()", uuidOsspMissing("uuid_generate_v4()", 8))
		available := func(v any) {
			run(fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='uuid-ossp'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"uuid-ossp", "1.1", v, "generate universally unique identifiers (UUIDs)"}}))
		}
		available(nil)
		run(fixture.Select("SELECT version,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='uuid-ossp' AND version='1.1'", []string{"version", "superuser", "trusted", "relocatable", "schema", "requires"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "NAME", "_NAME"}, [][]any{{"1.1", true, true, true, nil, nil}}))
		run(fixture.Exec("CREATE SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("ROLLBACK", 0))
		available(nil)
		uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part.uuid_generate_v4()", uuidOsspMissing("uuid_ossp_part.uuid_generate_v4()", 8))
		run(fixture.ImplicitError("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part VERSION 'missing'", fixture.Diagnostic{Code: "22023", Message: `extension "uuid-ossp" has no installation script nor update path for version "missing"`}))
		available(nil)
		run(fixture.Exec("CREATE USER uuid_ossp_owner", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec(`GRANT CREATE ON DATABASE "`+strings.ReplaceAll(database, `"`, `""`)+`" TO uuid_ossp_owner`, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION uuid_ossp_owner", 0))
		run(fixture.Exec("CREATE EXTENSION \"uuid-ossp\" SCHEMA uuid_ossp_part", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		available("1.1")
		uuidOsspInventory(t, p, c, "uuid_ossp_owner", bootstrapOwner)
		run(fixture.ImplicitError("CREATE EXTENSION \"uuid-ossp\"", fixture.Diagnostic{Code: "42710", Message: `extension "uuid-ossp" already exists`}))
		uuidOsspRead(t, p, c, r, pg, "SELECT uuid_ossp_part.uuid_generate_v4() AS u", 1, 1)
		uuidOsspFailure(t, p, c, "SELECT uuid_generate_v4()", uuidOsspMissing("uuid_generate_v4()", 8))
		run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
		q := "SELECT uuid_generate_v4() AS u"
		s, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		uuidOsspRead(t, p, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, pg, q, 1, 1)
		run(fixture.Exec("SET search_path TO public", 0))
		rows, err := s.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Fatal("off-path held statement returned rows")
		}
		d := uuidOsspMissing("uuid_generate_v4()", 8)
		fixture.AssertDiagnostic(t, err, &d)
		run(fixture.Exec("SET search_path TO uuid_ossp_part,public", 0))
		uuidOsspRead(t, p, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, pg, q, 1, 1)
		if pg {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA uuid_ossp_part FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION uuid_ossp_owner", 0))
		uuidOsspFailure(t, p, c, "SELECT uuid_ossp_part.uuid_generate_v4()", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema uuid_ossp_part", Position: "8"})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if pg {
			run(fixture.Exec("GRANT USAGE ON SCHEMA uuid_ossp_part TO uuid_ossp_owner", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA uuid_ossp_part OWNER TO uuid_ossp_owner", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION uuid_ossp_owner", 0))
		uuidOsspRead(t, p, c, r, pg, "SELECT uuid_ossp_part.uuid_generate_v4() AS u", 1, 1)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if !pg {
			run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='uuid_ossp_part' AND p.proname IN ('uuid_generate_v1','uuid_generate_v1mc','uuid_generate_v3','uuid_generate_v5','uuid_nil','uuid_ns_dns','uuid_ns_url','uuid_ns_oid','uuid_ns_x500')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}
