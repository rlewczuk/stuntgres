package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIssn13InstallAndIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "issn13_identity_ns")
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "issn13" does not exist`)
		want.Position = "31"
		run(fixture.ErrorDiagnostic("CREATE TABLE issn13_missing(v issn13)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE issn13_public(v public.issn13)", 0))
		run(fixture.Exec("CREATE TABLE issn13_unqualified(v issn13)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE issn13_public,issn13_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA issn13_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA issn13_identity_ns", 0))
		isbnIdentity(t, p, b, c, "issn13_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='issn13_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA issn13_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE issn13_qualified(v issn13_identity_ns.issn13,e issn13_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO issn13_qualified VALUES('12345670!','9780123456786')", 1))
		run(ean13Select(b, "SELECT v,e FROM issn13_qualified", []string{"v", "e"}, []string{"ISSN13", "EAN13"}, [][]any{{"977-1234-567-00-3!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "issn13_identity_ns")

	})
}

func TestReferenceIssn13Metadata(t *testing.T) {
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE issn13_meta(id integer NOT NULL,e ean13,v issn13,required issn13 NOT NULL)", 0))
		q := "SELECT e,v,required FROM issn13_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO issn13_meta VALUES(1,'9780123456786','12345679','12345670!'),(2,NULL,NULL,'9771234567898!')", 2))
				expected = [][]any{{"978-0-12-345678-6", "977-1234-567-00-3", "977-1234-567-00-3!"}, {nil, nil, "977-1234-567-89-8!"}}
			}
			s := ean13Select(b, q, []string{"e", "v", "required"}, []string{"EAN13", "ISSN13", "ISSN13"}, expected, []bool{true, true, false})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var e, v sql.NullString
				var required string
				if err := rows.Scan(&e, &v, &required); err != nil {
					t.Fatal(err)
				}
				wantRequired := expected[i][2]
				if e.Valid != (expected[i][0] != nil) || (e.Valid && e.String != expected[i][0]) || v.Valid != (expected[i][1] != nil) || (v.Valid && v.String != expected[i][1]) || required != wantRequired {
					t.Fatalf("specialized scans = %#v/%#v/%q, want %#v", e, v, required, expected[i])
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		empty := ean13Select(b, "SELECT e,v,required FROM issn13_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "ISSN13", "ISSN13"}, [][]any{}, []bool{true, true, false})
		run(empty)
		run(fixture.Prepared(empty))
		emptyHeld := ean13Held(t, p, c, empty.Query)
		fixture.RunStep(t, p.Ctx, emptyHeld, b.Recorder, empty)
		nullOnly := ean13Select(b, "SELECT v FROM issn13_meta WHERE id=2", []string{"v"}, []string{"ISSN13"}, [][]any{{nil}}, []bool{true})
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