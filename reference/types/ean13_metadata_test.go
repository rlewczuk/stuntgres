package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEan13InstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ean_identity_ns")
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "ean13" does not exist`)
		want.Position = "28"
		run(fixture.ErrorDiagnostic("CREATE TABLE ean_missing(v ean13)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ean_public(v public.ean13)", 0))
		run(fixture.Exec("CREATE TABLE ean_unqualified(v ean13)", 0))
		ean13Identity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE ean_public,ean_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA ean_identity_ns", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA ean_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE ean_qualified(v ean_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO ean_qualified VALUES('0123456789012')", 1))
		run(ean13Select(b, "SELECT v FROM ean_qualified", []string{"v"}, []string{"EAN13"}, [][]any{{"012-345678901-2"}}, []bool{true}))
		ean13Identity(t, p, b, c, "ean_identity_ns")
	})
}

func ean13Identity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, ns string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	var oid, array int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid="+ean13Literal(ns+".ean13")+"::regtype::oid").Scan(&oid, &array); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || array <= 0 || oid == array {
		t.Fatalf("invalid allocated identities %d/%d", oid, array)
	}
	q := fmt.Sprintf("SELECT t.typname,t.typtype,t.typcategory,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typtypmod,t.typcollation::bigint,a.typelem=t.oid AS reciprocal,t.typinput::oid=%s::regprocedure::oid AS input_identity,t.typoutput::oid=%s::regprocedure::oid AS output_identity FROM (SELECT * FROM pg_type WHERE oid=%d) t JOIN (SELECT * FROM pg_type WHERE oid=%d) a ON a.oid=t.typarray", ean13Literal(ns+".ean13_in(cstring)"), ean13Literal(ns+".ean13_out("+ns+".ean13)"), oid, array)
	s := fixture.Select(q, []string{"typname", "typtype", "typcategory", "typlen", "typbyval", "typalign", "typstorage", "typtypmod", "typcollation", "reciprocal", "input_identity", "output_identity"}, []string{"NAME", "CHAR", "CHAR", "INT2", "BOOL", "CHAR", "CHAR", "INT4", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{"ean13", "b", "U", int64(8), true, "d", "p", int64(-1), int64(0), true, true, true}})
	run(s)
	run(fixture.Prepared(s))
	var extensionOID int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&extensionOID); err != nil {
		t.Fatal(err)
	}
	membership := fmt.Sprintf("SELECT t.typnamespace=e.extnamespace AS same_namespace,t.typowner=e.extowner AS same_owner,d.deptype,e.extversion FROM (SELECT * FROM pg_type WHERE oid=%d) t JOIN (SELECT * FROM pg_depend WHERE classid=1247 AND objid=%d AND refclassid=3079 AND refobjid=%d) d ON d.objid=t.oid JOIN (SELECT * FROM pg_extension WHERE oid=%d) e ON e.oid=d.refobjid", oid, oid, extensionOID, extensionOID)
	run(fixture.Select(membership, []string{"same_namespace", "same_owner", "deptype", "extversion"}, []string{"BOOL", "BOOL", "CHAR", "TEXT"}, [][]any{{true, true, "e", "1.3"}}))
	for _, tc := range []struct{ name, arg, result string }{{"ean13_in", "cstring", ns + ".ean13"}, {"ean13_out", ns + ".ean13", "cstring"}} {
		var proc, ext int64
		if err := c.QueryRowContext(p.Ctx, "SELECT "+ean13Literal(ns+"."+tc.name+"("+tc.arg+")")+"::regprocedure::oid").Scan(&proc); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&ext); err != nil {
			t.Fatal(err)
		}
		q := fmt.Sprintf("SELECT p.proname,p.pronargs,p.prorettype=%s::regtype::oid AS result_identity,p.proisstrict,p.provolatile,p.proparallel,p.proowner=e.extowner AS same_owner,d.deptype FROM (SELECT * FROM pg_proc WHERE oid=%d) p JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND objid=%d AND refclassid=3079 AND refobjid=%d) d ON d.objid=p.oid JOIN (SELECT * FROM pg_extension WHERE oid=%d) e ON e.oid=d.refobjid", ean13Literal(tc.result), proc, proc, ext, ext)
		run(fixture.Select(q, []string{"proname", "pronargs", "result_identity", "proisstrict", "provolatile", "proparallel", "same_owner", "deptype"}, []string{"NAME", "INT2", "BOOL", "BOOL", "CHAR", "CHAR", "BOOL", "CHAR"}, [][]any{{tc.name, int64(1), true, true, "i", "s", true, "e"}}))
	}
	if !b.PG {
		s := fixture.Select("SELECT p.proname FROM pg_proc p WHERE p.pronamespace="+ean13Literal(ns)+"::regnamespace", []string{"proname"}, []string{"NAME"}, [][]any{{"ean13_in"}, {"ean13_out"}, {"isbn_in"}, {"isn_out"}, {"isbn13_in"}, {"ean13_out"}})
		s.Bag = true
		run(s)
	}
}

func TestReferenceEan13Metadata(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ean_meta(id integer NOT NULL,v ean13,required ean13 NOT NULL)", 0))
		q := "SELECT v,required FROM ean_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO ean_meta VALUES(1,'0123456789012','1234567890120!'),(2,NULL,'9780123456786')", 2))
				expected = [][]any{{"012-345678901-2", "123-456789012-8!"}, {nil, "978-0-12-345678-6"}}
			}
			s := ean13Select(b, q, []string{"v", "required"}, []string{"EAN13", "EAN13"}, expected, []bool{true, false})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var optional sql.NullString
				var required string
				if err := rows.Scan(&optional, &required); err != nil {
					t.Fatal(err)
				}
				if optional.Valid != (expected[i][0] != nil) || (optional.Valid && optional.String != expected[i][0]) || required != expected[i][1] {
					t.Fatalf("specialized scan = %v/%q, want %v", optional, required, expected[i])
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(ean13Select(b, "SELECT v FROM ean_meta WHERE id=2", []string{"v"}, []string{"EAN13"}, [][]any{{nil}}, []bool{true}))
		run(ean13Select(b, "SELECT v,required FROM ean_meta WHERE id=99", []string{"v", "required"}, []string{"EAN13", "EAN13"}, [][]any{}, []bool{true, false}))
		ean13Identity(t, p, b, c, "public")
	})
}
