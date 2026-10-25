package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func issn13InstallCodecCollisions(t *testing.T) {
	t.Run("input_signature", func(t *testing.T) {
		issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// This is genuine PostgreSQL internal-ABI setup, not a fake SQL
			// function or a new native loader capability. Native signature
			// admission is exercised by descriptor units in the production round.
			if !b.PG {
				return
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE FUNCTION issn13_in(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'", 0))
			var prior int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'issn13_in(cstring)'::regprocedure::oid").Scan(&prior); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "issn13_in" already exists with same argument types`)
			// Complete release-script Context, including the input declaration.
			want.Context = "SQL statement \"CREATE FUNCTION issn13_in(cstring)\n\tRETURNS issn13\n\tAS '$libdir/isn', 'issn_in'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 78"
			run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", want))
			issn13InstallUnpublished(t, p, b, c, "issn13_in")
			run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='issn13_in(cstring)'::regprocedure::oid", []string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, prior))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('issn13','_issn13')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("existing_type_output_signature", func(t *testing.T) {
		issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE issn13 AS ENUM ('occupied')", 0))
			run(fixture.Exec("CREATE FUNCTION ean13_out(issn13) RETURNS text LANGUAGE SQL AS 'SELECT ''occupied''::text'", 0))
			run(fixture.Exec("CREATE TABLE issn13_output_sentinel(id integer NOT NULL,v issn13 NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO issn13_output_sentinel VALUES(7,'occupied')", 1))
			var original, companion, routine, sentinel int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid='issn13'::regtype::oid").Scan(&original, &companion); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT 'ean13_out(issn13)'::regprocedure::oid,'issn13_output_sentinel'::regclass::oid").Scan(&routine, &sentinel); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "ean13_out" already exists with same argument types`)
			// Output collision precedes completion of the occupied scalar.
			want.Context = "SQL statement \"CREATE FUNCTION ean13_out(issn13)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 84"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT t.oid=$1::oid AS retained,t.typarray=$2::oid AS companion,t.typtype,a.typelem=t.oid AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='issn13'::regtype::oid",
					[]string{"retained", "companion", "typtype", "reciprocal"}, []string{"BOOL", "BOOL", "CHAR", "BOOL"}, [][]any{{true, true, "e", true}}, original, companion))
				run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='ean13_out(issn13)'::regprocedure::oid",
					[]string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, routine))
				run(fixture.Select("SELECT ean13_out('occupied'::issn13) AS original", []string{"original"}, []string{"TEXT"}, [][]any{{"occupied"}}))
				run(fixture.Select("SELECT id,v::text AS v,'issn13_output_sentinel'::regclass::oid=$1::oid AS retained FROM issn13_output_sentinel ORDER BY id",
					[]string{"id", "v", "retained"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "occupied", true}}, sentinel))
				issn13InstallUnpublished(t, p, b, c, "ean13_out")
				run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace ORDER BY typname",
					[]string{"typname"}, []string{"NAME"}, [][]any{{"_issn13"}, {"_issn13_output_sentinel"}, {"issn13"}, {"issn13_output_sentinel"}}))
			}
		})
	})
}