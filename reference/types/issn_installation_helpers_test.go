package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func issnInstallCodecCollisions(t *testing.T) {
	t.Run("input_signature", func(t *testing.T) {
		issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// This is genuine PostgreSQL internal-ABI setup, not a fake SQL
			// function or a new native loader capability. Native signature
			// admission is exercised by descriptor units in the production round.
			if !b.PG {
				return
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE FUNCTION issn_in(cstring) RETURNS text LANGUAGE internal IMMUTABLE STRICT AS 'textin'", 0))
			var prior int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'issn_in(cstring)'::regprocedure::oid").Scan(&prior); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "issn_in" already exists with same argument types`)
			// Complete release-script Context, including the input declaration.
			want.Context = "SQL statement \"CREATE FUNCTION issn_in(cstring)\n\tRETURNS issn\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 140"
			run(fixture.ErrorDiagnostic("CREATE EXTENSION isn", want))
			issnInstallUnpublished(t, p, b, c, "issn_in")
			run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='issn_in(cstring)'::regprocedure::oid", []string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, prior))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('issn','_issn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("existing_type_output_signature", func(t *testing.T) {
		issnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TYPE issn AS ENUM ('occupied')", 0))
			run(fixture.Exec("CREATE FUNCTION isn_out(issn) RETURNS text LANGUAGE SQL AS 'SELECT ''occupied''::text'", 0))
			run(fixture.Exec("CREATE TABLE issn_output_sentinel(id integer NOT NULL,v issn NOT NULL)", 0))
			run(fixture.Exec("INSERT INTO issn_output_sentinel VALUES(7,'occupied')", 1))
			var original, companion, routine, sentinel int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid,typarray FROM pg_type WHERE oid='issn'::regtype::oid").Scan(&original, &companion); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT 'isn_out(issn)'::regprocedure::oid,'issn_output_sentinel'::regclass::oid").Scan(&routine, &sentinel); err != nil {
				t.Fatal(err)
			}
			want := ean13Diagnostic("42723", `function "isn_out" already exists with same argument types`)
			// Output collision precedes completion of the occupied scalar.
			want.Context = "SQL statement \"CREATE FUNCTION isn_out(issn)\n\tRETURNS cstring\n\tAS '$libdir/isn'\n\tLANGUAGE C\n\tIMMUTABLE STRICT\n\tPARALLEL SAFE\"\nextension script file \"isn--1.1.sql\", near line 146"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("CREATE EXTENSION isn", want)
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.Select("SELECT t.oid=$1::oid AS retained,t.typarray=$2::oid AS companion,t.typtype,a.typelem=t.oid AS reciprocal FROM pg_type t JOIN pg_type a ON a.oid=t.typarray WHERE t.oid='issn'::regtype::oid",
					[]string{"retained", "companion", "typtype", "reciprocal"}, []string{"BOOL", "BOOL", "CHAR", "BOOL"}, [][]any{{true, true, "e", true}}, original, companion))
				run(fixture.Select("SELECT oid=$1::oid AS retained,prorettype='text'::regtype::oid AS result_identity FROM pg_proc WHERE oid='isn_out(issn)'::regprocedure::oid",
					[]string{"retained", "result_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, routine))
				run(fixture.Select("SELECT isn_out('occupied'::issn) AS original", []string{"original"}, []string{"TEXT"}, [][]any{{"occupied"}}))
				run(fixture.Select("SELECT id,v::text AS v,'issn_output_sentinel'::regclass::oid=$1::oid AS retained FROM issn_output_sentinel ORDER BY id",
					[]string{"id", "v", "retained"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(7), "occupied", true}}, sentinel))
				issnInstallUnpublished(t, p, b, c, "isn_out")
				run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace ORDER BY typname",
					[]string{"typname"}, []string{"NAME"}, [][]any{{"_issn"}, {"_issn_output_sentinel"}, {"issn"}, {"issn_output_sentinel"}}))
			}
		})
	})
}