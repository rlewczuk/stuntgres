package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsbn13InstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "isbn13_identity_ns")
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "isbn13" does not exist`)
		want.Position = "31"
		run(fixture.ErrorDiagnostic("CREATE TABLE isbn13_missing(v isbn13)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn13_public(v public.isbn13)", 0))
		run(fixture.Exec("CREATE TABLE isbn13_unqualified(v isbn13)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE isbn13_public,isbn13_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA isbn13_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA isbn13_identity_ns", 0))
		isbnIdentity(t, p, b, c, "isbn13_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='isbn13_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA isbn13_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE isbn13_qualified(v isbn13_identity_ns.isbn13,e isbn13_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO isbn13_qualified VALUES('1234567890!','9780123456786')", 1))
		run(ean13Select(b, "SELECT v,e FROM isbn13_qualified", []string{"v", "e"}, []string{"ISBN13", "EAN13"}, [][]any{{"978-1-234-56789-7!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "isbn13_identity_ns")
		stamp := isbnIdentityStamp(t, p, c, "isbn13_identity_ns.isbn13")
		bound := ean13Held(t, p, c, "SELECT ($1::isbn13_identity_ns.isbn13)::text AS v")
		for _, q := range []string{
			"ALTER FUNCTION isbn13_identity_ns.isbn13_in(cstring) RENAME TO longbook_in",
			"ALTER FUNCTION isbn13_identity_ns.ean13_out(isbn13_identity_ns.isbn13) RENAME TO longbook_out",
			"ALTER TYPE isbn13_identity_ns.isbn13 RENAME TO longbook",
		} {
			run(fixture.Exec(q, 0))
		}
		if got := isbnIdentityStamp(t, p, c, "isbn13_identity_ns.longbook"); got != stamp {
			t.Fatalf("renaming ISBN13/codecs changed immutable identity: %v, want %v", got, stamp)
		}
		for _, value := range []any{"1234567890!", []byte("1234567890!"), nil, []byte(nil)} {
			expected := [][]any{{"978-1-234-56789-7!"}}
			switch v := value.(type) {
			case nil:
				expected = [][]any{{nil}}
			case []byte:
				if v == nil {
					expected = [][]any{{nil}}
				}
			}
			fixture.RunStep(t, p.Ctx, bound, b.Recorder, fixture.Select(bound.SQL, []string{"v"}, []string{"TEXT"}, expected, value))
		}
		run(fixture.Select("SELECT typinput='isbn13_identity_ns.longbook_in(cstring)'::regprocedure::oid AS input_identity,typoutput='isbn13_identity_ns.longbook_out(isbn13_identity_ns.longbook)'::regprocedure::oid AS output_identity FROM pg_type WHERE oid='isbn13_identity_ns.longbook'::regtype::oid", []string{"input_identity", "output_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}

func TestReferenceIsbn13Metadata(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE isbn13_siblings(e ean13,s isbn,l isbn13,m ismn,n ismn13)", 0))
		run(fixture.Exec("INSERT INTO isbn13_siblings VALUES('9780123456786','123456789X','123456789X','M123456785','M123456785')", 1))
		run(ean13Select(b, "SELECT e,s,l,m,n FROM isbn13_siblings", []string{"e", "s", "l", "m", "n"}, []string{"EAN13", "ISBN", "ISBN13", "ISMN", "ISMN13"}, [][]any{{"978-0-12-345678-6", "1-234-56789-X", "978-1-234-56789-7", "M-1234-5678-5", "979-0-1234-5678-5"}}, []bool{true, true, true, true, true}))
		run(fixture.Exec("CREATE TABLE isbn13_meta(id integer NOT NULL,e ean13,v isbn13,required isbn13 NOT NULL)", 0))
		q := "SELECT e,v,required FROM isbn13_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO isbn13_meta VALUES(1,'9780123456786','123456789x','9780123456780!'),(2,NULL,NULL,'9791234567896')", 2))
				expected = [][]any{{"978-0-12-345678-6", "978-1-234-56789-7", "978-0-12-345678-6!"}, {nil, nil, "979-123456789-6"}}
			}
			s := ean13Select(b, q, []string{"e", "v", "required"}, []string{"EAN13", "ISBN13", "ISBN13"}, expected, []bool{true, true, false})
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
		run(ean13Select(b, "SELECT e,v,required FROM isbn13_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "ISBN13", "ISBN13"}, [][]any{}, []bool{true, true, false}))
		nullOnly := ean13Select(b, "SELECT v FROM isbn13_meta WHERE id=2", []string{"v"}, []string{"ISBN13"}, [][]any{{nil}}, []bool{true})
		run(nullOnly)
		run(fixture.Prepared(nullOnly))
		nullHeld := ean13Held(t, p, c, nullOnly.Query)
		fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, nullOnly)
		isbnIdentity(t, p, b, c, "public")
	})
}
