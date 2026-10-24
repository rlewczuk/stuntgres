package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsbnInstallCollisions(t *testing.T) {
	for _, tc := range []struct{ name, setup, kind string }{
		{"scalar", "CREATE TYPE isbn AS ENUM ('occupied')", "e"},
		{"domain", "CREATE DOMAIN isbn AS text", "d"},
		{"rowtype", "CREATE TABLE isbn(v integer)", "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(tc.setup, 0))
				run(fixture.Exec("CREATE TABLE isbn_sentinel(id integer NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO isbn_sentinel VALUES(7)", 1))
				var original, companion int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid='isbn'::regtype::oid").Scan(&original, &companion); err != nil {
					t.Fatal(err)
				}
				want := ean13Diagnostic("42710", `type "isbn" already exists`)
				// Complete extension-script Context observed on pinned18.6.
				want.Context = "SQL statement \"CREATE TYPE isbn (\n\tINPUT = isbn_in,\n\tOUTPUT = isn_out,\n\tLIKE = pg_catalog.int8\n)\"\nextension script file \"isn--1.1.sql\", near line 112"
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT oid=$1::oid AS retained,typarray=$2::oid AS companion,typtype FROM pg_type WHERE oid='isbn'::regtype::oid", []string{"retained", "companion", "typtype"}, []string{"BOOL", "BOOL", "CHAR"}, [][]any{{true, true, tc.kind}}, original, companion))
					isbnInstallUnpublished(t, p, b, c, "")
					run(fixture.Select("SELECT id FROM isbn_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			})
		})
	}
	// A generated _isbn companion cannot be independently renamed into this
	// state through public PostgreSQL DDL. Genuine descriptor-unit displacement
	// covers that installer entrance; public occupancy uses real enum/domain
	// scalars rather than pretending an unrelated scalar is an array.
	for _, tc := range []struct{ name, setup, kind string }{
		{"unrelated_array_name", "CREATE TYPE _isbn AS ENUM ('kept')", "e"},
		{"unrelated_domain_array_name", "CREATE DOMAIN _isbn AS text", "d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(tc.setup, 0))
				var prior int64
				if err := c.QueryRowContext(p.Ctx, "SELECT '_isbn'::regtype::oid").Scan(&prior); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				isbnIdentity(t, p, b, c, "public")
				run(fixture.Select("SELECT oid=$1::oid AS retained,typtype FROM pg_type WHERE oid='_isbn'::regtype::oid", []string{"retained", "typtype"}, []string{"BOOL", "CHAR"}, [][]any{{true, tc.kind}}, prior))
				run(fixture.Select("SELECT typarray<>$1::oid AS independent FROM pg_type WHERE oid='isbn'::regtype::oid", []string{"independent"}, []string{"BOOL"}, [][]any{{true}}, prior))
			})
		})
	}
	t.Run("input_signature", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// This is genuine PostgreSQL internal-ABI setup, not a fake SQL
			// function or a new native loader capability. Native signature
			// admission is exercised by descriptor units in the production round.
			if !b.PG {
				return
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE FUNCTION isbn_in(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'", 0))
			var prior int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'isbn_in(cstring)'::regprocedure::oid").Scan(&prior); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "isbn_in" already exists with same argument types`)
			want.Context = "SQL statement \"CREATE FUNCTION isbn_in(cstring)\n\tRETURNS isbn\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 100"
			run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", want))
			isbnInstallUnpublished(t, p, b, c, "isbn_in")
			run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='isbn_in(cstring)'::regprocedure::oid", []string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, prior))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('isbn','_isbn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("existing_type_output_signature", func(t *testing.T) {
		ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE isbn AS ENUM ('occupied')", 0))
			run(fixture.Exec("CREATE FUNCTION isn_out(isbn) RETURNS text LANGUAGE SQL AS 'SELECT ''occupied''::text'", 0))
			run(fixture.Exec("CREATE TABLE isbn_output_sentinel(id integer NOT NULL,v isbn NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO isbn_output_sentinel VALUES(7,'occupied')", 1))
			var original, companion, routine, sentinel int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid='isbn'::regtype::oid").Scan(&original, &companion); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT 'isn_out(isbn)'::regprocedure::oid,'isbn_output_sentinel'::regclass::oid").Scan(&routine, &sentinel); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "isn_out" already exists with same argument types`)
			want.Context = "SQL statement \"CREATE FUNCTION isn_out(isbn)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 106"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT t.oid=$1::oid AS retained,t.typarray=$2::oid AS companion,t.typtype,a.typelem=t.oid AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='isbn'::regtype::oid",
					[]string{"retained", "companion", "typtype", "reciprocal"}, []string{"BOOL", "BOOL", "CHAR", "BOOL"}, [][]any{{true, true, "e", true}}, original, companion))
				run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='isn_out(isbn)'::regprocedure::oid",
					[]string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, routine))
				run(fixture.Select("SELECT isn_out('occupied'::isbn) AS original", []string{"original"}, []string{"TEXT"}, [][]any{{"occupied"}}))
				run(fixture.Select("SELECT id,v::text AS v,'isbn_output_sentinel'::regclass::oid=$1::oid AS retained FROM isbn_output_sentinel ORDER BY id",
					[]string{"id", "v", "retained"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "occupied", true}}, sentinel))
				isbnInstallUnpublished(t, p, b, c, "isn_out")
				run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace ORDER BY typname",
					[]string{"typname"}, []string{"NAME"}, [][]any{{"_isbn"}, {"_isbn_output_sentinel"}, {"isbn"}, {"isbn_output_sentinel"}}))
			}
		})
	})
}

func isbnInstallUnpublished(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, preexistingRoutine string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13')", []string{"typname"}, []string{"NAME"}, [][]any{}))
	expected := [][]any{}
	if preexistingRoutine != "" {
		expected = [][]any{{preexistingRoutine}}
	}
	run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('ean13_in','ean13_out','isbn_in','isn_out','isbn13_in','ismn_in') ORDER BY proname", []string{"proname"}, []string{"NAME"}, expected))
}
