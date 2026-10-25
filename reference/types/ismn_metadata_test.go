package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsmnInstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ismn_identity_ns")
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "ismn" does not exist`)
		want.Position = "29"
		run(fixture.ErrorDiagnostic("CREATE TABLE ismn_missing(v ismn)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ismn_public(v public.ismn)", 0))
		run(fixture.Exec("CREATE TABLE ismn_unqualified(v ismn)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE ismn_public,ismn_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA ismn_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA ismn_identity_ns", 0))
		isbnIdentity(t, p, b, c, "ismn_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='ismn_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA ismn_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE ismn_qualified(v ismn_identity_ns.ismn,e ismn_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO ismn_qualified VALUES('M123456780!','9780123456786')", 1))
		run(ean13Select(b, "SELECT v,e FROM ismn_qualified", []string{"v", "e"}, []string{"ISMN", "EAN13"}, [][]any{{"M-1234-5678-5!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "ismn_identity_ns")
		stamp := isbnIdentityStamp(t, p, c, "ismn_identity_ns.ismn")
		bound := ean13Held(t, p, c, "SELECT ($1::ismn_identity_ns.ismn)::text AS v")
		for _, q := range []string{
			"ALTER FUNCTION ismn_identity_ns.ismn_in(cstring) RENAME TO music_in",
			"ALTER FUNCTION ismn_identity_ns.isn_out(ismn_identity_ns.ismn) RENAME TO music_out",
			"ALTER TYPE ismn_identity_ns.ismn RENAME TO music",
		} {
			run(fixture.Exec(q, 0))
		}
		if got := isbnIdentityStamp(t, p, c, "ismn_identity_ns.music"); got != stamp {
			t.Fatalf("renaming ISMN/codecs changed immutable identity: %v, want %v", got, stamp)
		}
		for _, value := range []any{"M123456780!", []byte("M123456780!"), nil, []byte(nil)} {
			expected := [][]any{{"M-1234-5678-5!"}}
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
		run(fixture.Select("SELECT typinput='ismn_identity_ns.music_in(cstring)'::regprocedure::oid AS input_identity,typoutput='ismn_identity_ns.music_out(ismn_identity_ns.music)'::regprocedure::oid AS output_identity FROM pg_type WHERE oid='ismn_identity_ns.music'::regtype::oid", []string{"input_identity", "output_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}

func TestReferenceIsmnMetadata(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ismn_siblings(e ean13,s isbn,l isbn13,m ismn,n ismn13)", 0))
		run(fixture.Exec("INSERT INTO ismn_siblings VALUES('9780123456786','123456789X','123456789X','M123456785','M123456785')", 1))
		run(ean13Select(b, "SELECT e,s,l,m,n FROM ismn_siblings", []string{"e", "s", "l", "m", "n"}, []string{"EAN13", "ISBN", "ISBN13", "ISMN", "ISMN13"}, [][]any{{"978-0-12-345678-6", "1-234-56789-X", "978-1-234-56789-7", "M-1234-5678-5", "979-0-1234-5678-5"}}, []bool{true, true, true, true, true}))
		run(fixture.Exec("CREATE TABLE ismn_meta(id integer NOT NULL,e ean13,v ismn,required ismn NOT NULL)", 0))
		q := "SELECT e,v,required FROM ismn_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO ismn_meta VALUES(1,'9780123456786','m123456785','9790123456780!'),(2,NULL,NULL,'M999999997')", 2))
				expected = [][]any{{"978-0-12-345678-6", "M-1234-5678-5", "M-1234-5678-5!"}, {nil, nil, "M-9999999-9-7"}}
			}
			s := ean13Select(b, q, []string{"e", "v", "required"}, []string{"EAN13", "ISMN", "ISMN"}, expected, []bool{true, true, false})
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
		empty := ean13Select(b, "SELECT e,v,required FROM ismn_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "ISMN", "ISMN"}, [][]any{}, []bool{true, true, false})
		run(empty)
		run(fixture.Prepared(empty))
		emptyHeld := ean13Held(t, p, c, empty.Query)
		fixture.RunStep(t, p.Ctx, emptyHeld, b.Recorder, empty)
		nullOnly := ean13Select(b, "SELECT v FROM ismn_meta WHERE id=2", []string{"v"}, []string{"ISMN"}, [][]any{{nil}}, []bool{true})
		run(nullOnly)
		run(fixture.Prepared(nullOnly))
		nullHeld := ean13Held(t, p, c, nullOnly.Query)
		fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, nullOnly)
		sourceEmpty := ean13Select(b, "SELECT prosrc FROM pg_proc WHERE oid=0", []string{"prosrc"}, []string{"TEXT"}, [][]any{}, []bool{false})
		run(sourceEmpty)
		run(fixture.Prepared(sourceEmpty))
		sourceHeld := ean13Held(t, p, c, sourceEmpty.Query)
		fixture.RunStep(t, p.Ctx, sourceHeld, b.Recorder, sourceEmpty)
		isbnIdentity(t, p, b, c, "public")
	})
}
