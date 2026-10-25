package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLevenshteinDiagnostics(t *testing.T) {
	t.Run("Public", func(t *testing.T) {
		levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			fail := func(s fixture.Step) {
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
			for _, tc := range []struct{ q, name, types string }{
				{"SELECT levenshtein('a','b')", "levenshtein", "unknown, unknown"},
				{"SELECT levenshtein_less_equal('a','b',1)", "levenshtein_less_equal", "unknown, unknown, integer"},
			} {
				fail(extensionMissing(tc.q, tc.name, tc.types))
			}
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
			for _, tc := range []struct{ q, name, types string }{
				{"SELECT levenshtein()", "levenshtein", ""},
				{"SELECT levenshtein('a','b',1)", "levenshtein", "unknown, unknown, integer"},
				{"SELECT levenshtein_less_equal('a','b')", "levenshtein_less_equal", "unknown, unknown"},
				{"SELECT levenshtein_less_equal('a','b',1,1)", "levenshtein_less_equal", "unknown, unknown, integer, integer"},
				{"SELECT levenshtein(1,'b')", "levenshtein", "integer, unknown"},
				{"SELECT levenshtein('a',1)", "levenshtein", "unknown, integer"},
				{"SELECT levenshtein('a','b',1::bigint,1,1)", "levenshtein", "unknown, unknown, bigint, integer, integer"},
				{"SELECT levenshtein('a','b',1.5,1,1)", "levenshtein", "unknown, unknown, numeric, integer, integer"},
				{"SELECT levenshtein_less_equal('a','b',true)", "levenshtein_less_equal", "unknown, unknown, boolean"},
				{"SELECT levenshtein(source => 'a',target => 'b')", "levenshtein", "source => unknown, target => unknown"},
				{"SELECT levenshtein('a',target => 'b')", "levenshtein", "unknown, target => unknown"},
				{"SELECT public.levenshtein('a',target => 'b')", "public.levenshtein", "unknown, target => unknown"},
				{"SELECT levenshtein('a','b',ins_cost => 1,del_cost => 1,sub_cost => 1)", "levenshtein", "unknown, unknown, ins_cost => integer, del_cost => integer, sub_cost => integer"},
				{"SELECT levenshtein('a','b',1,1,sub_cost => 1)", "levenshtein", "unknown, unknown, integer, integer, sub_cost => integer"},
				{"SELECT levenshtein_less_equal('a','b',max_d => 1)", "levenshtein_less_equal", "unknown, unknown, max_d => integer"},
				{"SELECT public.levenshtein_less_equal('a','b',max_d => 1)", "public.levenshtein_less_equal", "unknown, unknown, max_d => integer"},
				{"SELECT levenshtein_less_equal('a','b',1,1,1,max_d => 1)", "levenshtein_less_equal", "unknown, unknown, integer, integer, integer, max_d => integer"},
				{"SELECT public.levenshtein_less_equal('a','b',1,1,1,max_d => 1)", "public.levenshtein_less_equal", "unknown, unknown, integer, integer, integer, max_d => integer"},
				{`SELECT "Levenshtein"('a','b')`, "Levenshtein", "unknown, unknown"},
			} {
				fail(extensionMissing(tc.q, tc.name, tc.types))
			}
			q := "SELECT levenshtein(source => 'a','b')"
			fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: fixture.Position(strings.LastIndex(q, "'b'") + 1)}))
			fail(fixture.ImplicitError("SELECT lev_missing.levenshtein('a','b')", fixture.Diagnostic{Code: "3F000", Message: `schema "lev_missing" does not exist`, Position: "8"}))
			for _, name := range []string{"levenshtein", "public.levenshtein", "levenshtein_less_equal", "public.levenshtein_less_equal"} {
				args := "'a','b'"
				if strings.HasSuffix(name, "less_equal") {
					args += ",1"
				}
				for _, tc := range []struct{ suffix, decoration, kind string }{
					{"(DISTINCT " + args + ")", "DISTINCT", "an aggregate function"},
					{"(" + args + " ORDER BY 'a')", "ORDER BY", "an aggregate function"},
					{"(" + args + ") FILTER (WHERE true)", "FILTER", "an aggregate function"},
					{"(" + args + ") OVER ()", "OVER", "a window function nor an aggregate function"},
				} {
					fail(fixture.ImplicitError("SELECT "+name+tc.suffix, fixture.Diagnostic{Code: "42809", Message: tc.decoration + " specified, but " + name + " is not " + tc.kind, Position: "8"}))
				}
			}
			run(levenshteinSelect("SELECT levenshtein(ALL 'a','b')", "levenshtein", int64(1)))
			invalid := "SELECT levenshtein('a','b','bad',1,1)"
			fail(fixture.ImplicitError(invalid, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: fixture.Position(strings.Index(invalid, "'bad'") + 1)}))
			// The pinned oracle establishes this conversion Context; the native
			// adapter's existing frameImporter preserves the same input contract.
			for _, tc := range []struct {
				query string
				args  []any
			}{
				{"SELECT levenshtein($1,$2,$3,$4,$5)", []any{"a", "b", "bad", 1, 1}},
				{"SELECT levenshtein_less_equal($1,$2,$3)", []any{"a", "b", "bad"}},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $3 = '...'"}, tc.args...)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					run(s)
				}
			}
			run(fixture.Exec("SET search_path TO pg_catalog", 0))
			fail(extensionMissing("SELECT levenshtein('a','b')", "levenshtein", "unknown, unknown"))
			fail(extensionMissing("SELECT levenshtein_less_equal('a','b',1)", "levenshtein_less_equal", "unknown, unknown, integer"))
			run(levenshteinSelect("SELECT public.levenshtein('a','b')", "levenshtein", int64(1)))
			run(fixture.Exec("RESET search_path", 0))
		})
	})
	t.Run("Relocated", func(t *testing.T) {
		levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			fail := func(s fixture.Step) {
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
			run(fixture.Exec("CREATE SCHEMA lev_addon", 0))
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA lev_addon", 0))
			fail(extensionMissing("SELECT lev_addon.levenshtein('a',target => 'b')", "lev_addon.levenshtein", "unknown, target => unknown"))
			fail(extensionMissing("SELECT lev_addon.levenshtein_less_equal('a','b',1,1,1,max_d => 1)", "lev_addon.levenshtein_less_equal", "unknown, unknown, integer, integer, integer, max_d => integer"))
			run(fixture.Exec("CREATE USER lev_reader NOSUPERUSER", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION lev_reader", 0))
			fail(fixture.ImplicitError("SELECT lev_addon.levenshtein('a','b')", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema lev_addon", Position: "8"}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			// Native schema ACL GRANT is absent; transfer schema ownership for the
			// successful visibility phase, preserving the real PG USAGE denial above.
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA lev_addon TO lev_reader", 0))
			} else {
				run(fixture.Exec("ALTER SCHEMA lev_addon OWNER TO lev_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION lev_reader", 0))
			run(levenshteinSelect("SELECT lev_addon.levenshtein('a','b')", "levenshtein", int64(1)))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
}
