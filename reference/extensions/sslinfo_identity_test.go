package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,r.rolname=current_user AS executing_owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='sslinfo'",
		[]string{"extversion", "extrelocatable", "nspname", "executing_owner"}, []string{"TEXT", "BOOL", "NAME", "BOOL"}, [][]any{{"1.2", true, "ssl_part", true}}))
	want := [][]any{
		{"ssl_cipher", int64(0), int64(25), true, "v", "r", "f", "c", "ssl_cipher", "$libdir/sslinfo", true, true, true},
		{"ssl_client_cert_present", int64(0), int64(16), true, "v", "r", "f", "c", "ssl_client_cert_present", "$libdir/sslinfo", true, true, true},
		{"ssl_client_dn", int64(0), int64(25), true, "v", "r", "f", "c", "ssl_client_dn", "$libdir/sslinfo", true, true, true},
		{"ssl_client_serial", int64(0), int64(1700), true, "v", "r", "f", "c", "ssl_client_serial", "$libdir/sslinfo", true, true, true},
		{"ssl_is_used", int64(0), int64(16), true, "v", "r", "f", "c", "ssl_is_used", "$libdir/sslinfo", true, true, true},
		{"ssl_issuer_dn", int64(0), int64(25), true, "v", "r", "f", "c", "ssl_issuer_dn", "$libdir/sslinfo", true, true, true},
		{"ssl_version", int64(0), int64(25), true, "v", "r", "f", "c", "ssl_version", "$libdir/sslinfo", true, true, true},
	}
	q := "SELECT p.proname,p.pronargs,p.prorettype::bigint AS result_oid,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prokind::text AS kind,l.lanname,p.prosrc,p.probin,p.oid>0 AS allocated,p.pronamespace=e.extnamespace AS same_schema,r.rolname=current_user AS executing_owner FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.oid=p.proowner JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='sslinfo' AND p.proname IN (" + sslInfoNames + ") ORDER BY p.proname"
	for _, prepared := range []bool{false, true} {
		s := fixture.Select(q,
			[]string{"proname", "pronargs", "result_oid", "proisstrict", "volatility", "parallel", "kind", "lanname", "prosrc", "probin", "allocated", "same_schema", "executing_owner"},
			[]string{"NAME", "INT2", "INT8", "BOOL", "TEXT", "TEXT", "TEXT", "NAME", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL"}, want)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func sslInfoIdentities(t *testing.T, p *fixture.Pair, c *sql.Conn) []int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ssl_part' AND p.proname IN ("+sslInfoNames+") ORDER BY p.proname")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil || id <= 0 {
			t.Fatal("allocated identity", id, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 7 {
		t.Fatalf("selected identities=%v", ids)
	}
	return ids
}

func TestReferenceSSLInfoIdentity(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='sslinfo'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"sslinfo", "1.2", nil, "information about SSL certificates"}}))
		run(fixture.Select("SELECT version,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='sslinfo' AND version='1.2'", []string{"version", "superuser", "trusted", "relocatable", "schema", "requires"}, []string{"TEXT", "BOOL", "BOOL", "BOOL", "NAME", "_NAME"}, [][]any{{"1.2", true, false, true, nil, nil}}))
		run(fixture.Exec("CREATE SCHEMA ssl_part", 0))
		run(fixture.Exec("CREATE ROLE ssl_owner", 0))
		run(fixture.Exec("SET ROLE ssl_owner", 0))
		run(fixture.ImplicitError("CREATE EXTENSION sslinfo SCHEMA ssl_part", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "sslinfo"`, Hint: "Must be superuser to create this extension."}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='sslinfo'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ssl_part'", []string{"proname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE EXTENSION sslinfo SCHEMA ssl_part VERSION '1.2'", 0))
		sslInfoInventory(t, p, c, b.Recorder)
		before := sslInfoIdentities(t, p, c)
		run(fixture.ImplicitError("CREATE EXTENSION sslinfo", fixture.Diagnostic{Code: "42710", Message: `extension "sslinfo" already exists`}))
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			notices = nil
			s := fixture.Exec("CREATE EXTENSION IF NOT EXISTS sslinfo SCHEMA missing_ssl_schema VERSION 'missing'", 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, nr, s)
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "sslinfo" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
		}
		if after := sslInfoIdentities(t, p, c); !reflect.DeepEqual(before, after) {
			t.Fatalf("repeat installation changed identities: %v -> %v", before, after)
		}
		sslInfoInventory(t, p, c, b.Recorder)
		if !b.PG {
			run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid=3079 AND d.deptype='e' AND e.extname='sslinfo'", []string{"count"}, []string{"INT8"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ssl_part' AND p.proname IN ('ssl_client_dn_field','ssl_issuer_field','ssl_extension_info')", []string{"proname"}, []string{"NAME"}, nil))
		}
	})
}

func TestReferenceSSLInfoResolutionAndTransactions(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_is_used()", sslInfoMissing("ssl_is_used()", 8))
		run(fixture.Exec("CREATE SCHEMA ssl_part", 0))
		run(fixture.Exec("CREATE TABLE ssl_sentinel(id integer)", 0))
		run(fixture.Exec("INSERT INTO ssl_sentinel VALUES(7)", 1))
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION sslinfo SCHEMA ssl_part", 0))
		run(sslInfoSelect("SELECT " + sslInfoProjection))
		sslInfoFailure(t, p, peer, b.Recorder, "SELECT ssl_part.ssl_is_used()", sslInfoMissing("ssl_part.ssl_is_used()", 8))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='sslinfo'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ssl_part' AND p.proname IN ("+sslInfoNames+")", []string{"proname"}, []string{"NAME"}, nil))
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_part.ssl_is_used()", sslInfoMissing("ssl_part.ssl_is_used()", 8))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ssl_install", 0))
		run(fixture.Exec("CREATE EXTENSION sslinfo SCHEMA ssl_part", 0))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ssl_install", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='sslinfo'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE EXTENSION sslinfo SCHEMA ssl_part", 0))
		run(fixture.Exec("COMMIT", 0))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, sslInfoSelect("SELECT "+sslInfoProjection))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(sslInfoSelect("SELECT "+sslInfoProjection)))
		run(fixture.Exec("SET search_path TO ssl_part,public", 0))
		for _, fn := range sslInfoFunctions {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT "+fn.name+"() AS value", []string{"value"}, []string{fn.typ}, [][]any{{fn.value}})
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("SET search_path TO public", 0))
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_is_used()", sslInfoMissing("ssl_is_used()", 8))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT ssl_error", 0))
		s := fixture.ErrorDiagnostic("SELECT ssl_part.ssl_is_used(NULL)", sslInfoMissing("ssl_part.ssl_is_used(unknown)", 8))
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		run(fixture.ImplicitError("SELECT id FROM ssl_sentinel", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO SAVEPOINT ssl_error", 0))
		run(sslInfoSelect("SELECT " + sslInfoProjection))
		run(fixture.Exec("UPDATE ssl_sentinel SET id=8", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT id FROM ssl_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(sslInfoSelect("SELECT " + sslInfoProjection))
	})
}
