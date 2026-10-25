package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUpcInstallAndIdentity(t *testing.T) {
	t.Run("identity", func(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "upc_identity_ns")
	upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		want := ean13Diagnostic("42704", `type "upc" does not exist`)
		want.Position = "28"
		run(fixture.ErrorDiagnostic("CREATE TABLE upc_missing(v upc)", want))
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE upc_public(v public.upc)", 0))
		run(fixture.Exec("CREATE TABLE upc_unqualified(v upc)", 0))
		isbnIdentity(t, p, b, c, "public")
		duplicate := ean13Diagnostic("42710", `extension "isn" already exists`)
		run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", duplicate))
		run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS isn", 0))
		isbnIdentity(t, p, b, c, "public")
		run(fixture.Exec("DROP TABLE upc_public,upc_unqualified", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		run(fixture.Exec("CREATE SCHEMA upc_identity_ns", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA upc_identity_ns", 0))
		isbnIdentity(t, p, b, c, "upc_identity_ns")
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='upc_identity_ns'::regnamespace", []string{"typname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Exec("CREATE EXTENSION isn SCHEMA upc_identity_ns", 0))
		run(fixture.Exec("CREATE TABLE upc_qualified(v upc_identity_ns.upc,e upc_identity_ns.ean13)", 0))
		run(fixture.Exec("INSERT INTO upc_qualified VALUES('123456789010!','9780123456786')", 1))
		run(upcSelect(b, "SELECT v,e FROM upc_qualified", []string{"v", "e"}, []string{"UPC", "EAN13"}, [][]any{{"123456789012!", "978-0-12-345678-6"}}, []bool{true, true}))
		isbnIdentity(t, p, b, c, "upc_identity_ns")

	})
	})
	for _, tc := range []struct{ name, setup, code, message, context string }{
		{"type_collision", "CREATE TYPE upc AS ENUM ('occupied')", "42710", `type "upc" already exists`, "SQL statement \"CREATE TYPE upc (\n\tINPUT = upc_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 172"},
		{"input_collision", "CREATE EXTENSION citext; ALTER FUNCTION public.citextin(cstring) RENAME TO upc_in", "42723", `function "upc_in" already exists with same argument types`, "SQL statement \"CREATE FUNCTION upc_in(cstring)\n\tRETURNS upc\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 160"},
		{"output_collision", "CREATE TYPE upc AS ENUM ('occupied'); CREATE FUNCTION isn_out(upc) RETURNS text LANGUAGE SQL AS 'SELECT ''occupied''::text'", "42723", `function "isn_out" already exists with same argument types`, "SQL statement \"CREATE FUNCTION isn_out(upc)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 166"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(tc.setup, 0))
				want := ean13Diagnostic(tc.code, tc.message)
				want.Context = tc.context
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
					if prepared { s = fixture.Prepared(s) }
					run(s)
					run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
					run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn','issn13','_issn13')", []string{"typname"}, []string{"NAME"}, [][]any{}))
				}
			})
		})
	}
}

func TestReferenceUpcMetadata(t *testing.T) {
	upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE upc_meta(id integer NOT NULL,e ean13,v upc,required upc NOT NULL)", 0))
		q := "SELECT e,v,required FROM upc_meta ORDER BY id"
		held := ean13Held(t, p, c, q)
		for _, populated := range []bool{false, true} {
			expected := [][]any{}
			if populated {
				run(fixture.Exec("INSERT INTO upc_meta VALUES(1,'9780123456786','123456789012','123456789010!'),(2,NULL,NULL,'0999999999993!')", 2))
				expected = [][]any{{"978-0-12-345678-6", "123456789012", "123456789012!"}, {nil, nil, "999999999993!"}}
			}
			s := upcSelect(b, q, []string{"e", "v", "required"}, []string{"EAN13", "UPC", "UPC"}, expected, []bool{true, true, false})
			s.CheckScan = func(t *testing.T, rows *sql.Rows, i int) {
				var e, v sql.NullString
				var required string
				if err := rows.Scan(&e, &v, &required); err != nil {
					t.Fatal(err)
				}
				wantRequired := upcCell(b, expected[i][2])
				if e.Valid != (expected[i][0] != nil) || (e.Valid && e.String != expected[i][0]) || v.Valid != (expected[i][1] != nil) || (v.Valid && v.String != expected[i][1]) || required != wantRequired {
					t.Fatalf("specialized scans = %#v/%#v/%q, want %#v", e, v, required, expected[i])
				}
			}
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		empty := upcSelect(b, "SELECT e,v,required FROM upc_meta WHERE id=99", []string{"e", "v", "required"}, []string{"EAN13", "UPC", "UPC"}, [][]any{}, []bool{true, true, false})
		run(empty)
		run(fixture.Prepared(empty))
		emptyHeld := ean13Held(t, p, c, empty.Query)
		fixture.RunStep(t, p.Ctx, emptyHeld, b.Recorder, empty)
		nullOnly := upcSelect(b, "SELECT v FROM upc_meta WHERE id=2", []string{"v"}, []string{"UPC"}, [][]any{{nil}}, []bool{true})
		run(nullOnly)
		run(fixture.Prepared(nullOnly))
		nullHeld := ean13Held(t, p, c, nullOnly.Query)
		fixture.RunStep(t, p.Ctx, nullHeld, b.Recorder, nullOnly)
		sourceEmpty := upcSelect(b, "SELECT prosrc FROM pg_proc WHERE oid=0", []string{"prosrc"}, []string{"TEXT"}, [][]any{}, []bool{false})
		run(sourceEmpty)
		run(fixture.Prepared(sourceEmpty))
		sourceHeld := ean13Held(t, p, c, sourceEmpty.Query)
		fixture.RunStep(t, p.Ctx, sourceHeld, b.Recorder, sourceEmpty)
		isbnIdentity(t, p, b, c, "public")
	})
}