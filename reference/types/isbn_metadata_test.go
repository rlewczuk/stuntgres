package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsbnInstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "isbn_identity_ns")
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "isbn" does not exist`)
		want.Position = "29"
		run(fixture.ErrorDiagnostic("CREATE TABLE isbn_missing(v isbn)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn_public(v public.isbn)", 0))
		run(fixture.Exec("CREATE TABLE isbn_unqualified(v isbn)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE isbn_public,isbn_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA isbn_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA isbn_identity_ns", 0))
		isbnIdentity(t, p, b, c, "isbn_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='isbn_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA isbn_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE isbn_qualified(v isbn_identity_ns.isbn,e isbn_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO isbn_qualified VALUES('1234567890!','9780123456786')", 1))
		run(ean13Select(b, "SELECT v,e FROM isbn_qualified", []string{"v", "e"}, []string{"ISBN", "EAN13"}, [][]any{{"1-234-56789-X!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "isbn_identity_ns")
	})
}

// Every identity is relational: no allocator value or EAN codec is an ISBN
// oracle. Verify both scalars together so accidental member-zero reuse fails.
func isbnIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, ns string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	seen := map[int64]string{}
	for _, role := range []struct{ scalar, input, output string }{{"ean13", "ean13_in", "ean13_out"}, {"isbn", "isbn_in", "isn_out"}} {
		name := ns + "." + role.scalar
		var oid, array, input, output int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid="+ean13Literal(name)+"::regtype::oid").Scan(&oid, &array, &input, &output); err != nil {
			t.Fatal(err)
		}
		for _, id := range []int64{oid, array, input, output} {
			if id <= 0 || seen[id] != "" {
				t.Fatalf("%s identity %d is invalid or shared with %s", name, id, seen[id])
			}
			seen[id] = name
		}
		q := fmt.Sprintf("SELECT t.typname,t.typtype,t.typcategory,t.typlen,t.typbyval,t.typalign,t.typstorage,t.typtypmod,t.typcollation::bigint,a.typelem=t.oid AS reciprocal,t.typinput=%s::regprocedure::oid AS input_identity,t.typoutput=%s::regprocedure::oid AS output_identity FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid=%d", ean13Literal(ns+"."+role.input+"(cstring)"), ean13Literal(ns+"."+role.output+"("+name+")"), oid)
		s := fixture.Select(q, []string{"typname", "typtype", "typcategory", "typlen", "typbyval", "typalign", "typstorage", "typtypmod", "typcollation", "reciprocal", "input_identity", "output_identity"}, []string{"NAME", "CHAR", "CHAR", "INT2", "BOOL", "CHAR", "CHAR", "INT4", "INT8", "BOOL", "BOOL", "BOOL"}, [][]any{{role.scalar, "b", "U", int64(8), true, "d", "p", int64(-1), int64(0), true, true, true}})
		run(s)
		run(fixture.Prepared(s))
		run(fixture.Select(fmt.Sprintf("SELECT t.typnamespace=e.extnamespace AS same_namespace,t.typowner=e.extowner AS same_owner,d.deptype,e.extversion FROM pg_type t JOIN pg_depend d ON d.objid=t.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE t.oid=%d AND d.classid=1247 AND d.refclassid=3079 AND e.extname='isn'", oid), []string{"same_namespace", "same_owner", "deptype", "extversion"}, []string{"BOOL", "BOOL", "CHAR", "TEXT"}, [][]any{{true, true, "e", "1.3"}}))
		for _, codec := range []struct {
			id int64
			name, arg, result string
		}{{input, role.input, "cstring", name}, {output, role.output, name, "cstring"}} {
			q := fmt.Sprintf("SELECT p.proname,p.pronargs,p.oid=%s::regprocedure::oid AS argument_identity,p.prorettype=%s::regtype::oid AS result_identity,p.proisstrict,p.provolatile,p.proparallel,l.lanname,p.proowner=e.extowner AS same_owner,d.deptype FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE p.oid=%d AND d.classid=1255 AND d.refclassid=3079 AND e.extname='isn'", ean13Literal(ns+"."+codec.name+"("+codec.arg+")"), ean13Literal(codec.result), codec.id)
			run(fixture.Select(q, []string{"proname", "pronargs", "argument_identity", "result_identity", "proisstrict", "provolatile", "proparallel", "lanname", "same_owner", "deptype"}, []string{"NAME", "INT2", "BOOL", "BOOL", "BOOL", "CHAR", "CHAR", "NAME", "BOOL", "CHAR"}, [][]any{{codec.name, int64(1), true, true, true, "i", "s", "c", true, "e"}}))
			// Each routine depends normally on its own scalar, not the other
			// manifest role. Builtin cstring is pinned and has no normal edge.
			run(fixture.Select(fmt.Sprintf("SELECT refobjid=%d::oid AS own_scalar,deptype FROM pg_depend WHERE classid=1255 AND objid=%d AND refclassid=1247", oid, codec.id), []string{"own_scalar", "deptype"}, []string{"BOOL", "CHAR"}, [][]any{{true, "n"}}))
		}
	}
	if !b.PG {
		s := fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace="+ean13Literal(ns)+"::regnamespace", []string{"proname"}, []string{"NAME"}, [][]any{{"ean13_in"}, {"ean13_out"}, {"isbn_in"}, {"isn_out"}})
		s.Bag = true
		run(s)
	}
}

func TestReferenceIsbnMetadata(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn_meta(id integer NOT NULL,e ean13,v isbn,required isbn NOT NULL)", 0))
		q := "SELECT e,v,required FROM isbn_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO isbn_meta VALUES(1,'9780123456786','123456789x','9780123456780!'),(2,NULL,NULL,'9791234567896')", 2))
				expected = [][]any{{"978-0-12-345678-6", "1-234-56789-X", "0-12-345678-9!"}, {nil, nil, "979-123456789-6"}}
			}
			s := ean13Select(b, q, []string{"e", "v", "required"}, []string{"EAN13", "ISBN", "ISBN"}, expected, []bool{true, true, false})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var e, v sql.NullString
				var required string
				if err := rows.Scan(&e, &v, &required); err != nil {
					t.Fatal(err)
				}
				if e.Valid != (expected[i][0] != nil) || (e.Valid && e.String != expected[i][0]) || v.Valid != (expected[i][1] != nil) || (v.Valid && v.String != expected[i][1]) || required != expected[i][2] {
					t.Fatalf("specialized scans = %#v/%#v/%q, want %#v", e, v, required, expected[i])
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(ean13Select(b, "SELECT e,v,required FROM isbn_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "ISBN", "ISBN"}, [][]any{}, []bool{true, true, false}))
		isbnIdentity(t, p, b, c, "public")
	})
}
