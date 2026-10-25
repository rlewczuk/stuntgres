package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIssnInstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "issn_identity_ns")
	issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "issn" does not exist`)
		want.Position = "29"
		run(fixture.ErrorDiagnostic("CREATE TABLE issn_missing(v issn)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE issn_public(v public.issn)", 0))
		run(fixture.Exec("CREATE TABLE issn_unqualified(v issn)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE issn_public,issn_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA issn_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA issn_identity_ns", 0))
		isbnIdentity(t, p, b, c, "issn_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='issn_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA issn_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE issn_qualified(v issn_identity_ns.issn,e issn_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO issn_qualified VALUES('12345670!','9780123456786')", 1))
		run(issnSelect(b, "SELECT v,e FROM issn_qualified", []string{"v", "e"}, []string{"ISSN", "EAN13"}, [][]any{{"1234-5679!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "issn_identity_ns")

	})
}

func TestReferenceIssnMetadata(t *testing.T) {
	issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE issn_siblings(e ean13,s isbn,l isbn13,m ismn,n ismn13,sn issn)", 0))
		run(fixture.Exec("INSERT INTO issn_siblings VALUES('9780123456786','123456789X','123456789X','M123456785','M123456785','12345679')", 1))
		run(issnSelect(b, "SELECT e,s,l,m,n,sn FROM issn_siblings", []string{"e", "s", "l", "m", "n", "sn"}, []string{"EAN13", "ISBN", "ISBN13", "ISMN", "ISMN13", "ISSN"}, [][]any{{"978-0-12-345678-6", "1-234-56789-X", "978-1-234-56789-7", "M-1234-5678-5", "979-0-1234-5678-5", "1234-5679"}}, []bool{true, true, true, true, true, true}))
		run(fixture.Exec("CREATE TABLE issn_meta(id integer NOT NULL,e ean13,v issn,required issn NOT NULL)", 0))
		q := "SELECT e,v,required FROM issn_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO issn_meta VALUES(1,'9780123456786','12345679','12345670!'),(2,NULL,NULL,'9771234567898!')", 2))
				expected = [][]any{{"978-0-12-345678-6", "1234-5679", "1234-5679!"}, {nil, nil, "977-1234-567-89-8!"}}
			}
			s := issnSelect(b, q, []string{"e", "v", "required"}, []string{"EAN13", "ISSN", "ISSN"}, expected, []bool{true, true, false})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var e, v sql.NullString
				var required string
				if err := rows.Scan(&e, &v, &required); err != nil {
					t.Fatal(err)
				}
				wantRequired := expected[i][2]
				if text, ok := wantRequired.(string); ok { wantRequired = issnOutput(b, text) }
				if e.Valid != (expected[i][0] != nil) || (e.Valid && e.String != expected[i][0]) || v.Valid != (expected[i][1] != nil) || (v.Valid && v.String != expected[i][1]) || required != wantRequired {
					t.Fatalf("specialized scans = %#v/%#v/%q, want %#v", e, v, required, expected[i])
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		empty := issnSelect(b, "SELECT e,v,required FROM issn_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "ISSN", "ISSN"}, [][]any{}, []bool{true, true, false})
		run(empty)
		run(fixture.Prepared(empty))
		emptyHeld := ean13Held(t, p, c, empty.Query)
		fixture.RunStep(t, p.Ctx, emptyHeld, b.Recorder, empty)
		nullOnly := issnSelect(b, "SELECT v FROM issn_meta WHERE id=2", []string{"v"}, []string{"ISSN"}, [][]any{{nil}}, []bool{true})
		run(nullOnly)
		run(fixture.Prepared(nullOnly))
		nullHeld := ean13Held(t, p, c, nullOnly.Query)
		fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, nullOnly)
		sourceEmpty := issnSelect(b, "SELECT prosrc FROM pg_proc WHERE oid=0", []string{"prosrc"}, []string{"TEXT"}, [][]any{}, []bool{false})
		run(sourceEmpty)
		run(fixture.Prepared(sourceEmpty))
		sourceHeld := ean13Held(t, p, c, sourceEmpty.Query)
		fixture.RunStep(t, p.Ctx, sourceHeld, b.Recorder, sourceEmpty)
		isbnIdentity(t, p, b, c, "public")
	})
}