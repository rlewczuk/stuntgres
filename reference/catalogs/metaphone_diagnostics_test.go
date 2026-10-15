package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMetaphoneDiagnostics(t *testing.T) {
	t.Run("Public", func(t *testing.T) {
		metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			fail := func(s fixture.Step) {
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
			for _, tc := range []struct{ name, args, types string }{
				{"metaphone", "'a',4", "unknown, integer"}, {"dmetaphone", "'a'", "unknown"}, {"dmetaphone_alt", "'a'", "unknown"},
			} {
				fail(extensionMissing("SELECT "+tc.name+"("+tc.args+")", tc.name, tc.types))
			}
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
			for _, tc := range []struct{ q, name, types string }{
				{"SELECT metaphone()", "metaphone", ""},
				{"SELECT metaphone('a')", "metaphone", "unknown"},
				{"SELECT metaphone('a',4,4)", "metaphone", "unknown, integer, integer"},
				{"SELECT metaphone(1,4)", "metaphone", "integer, integer"},
				{"SELECT metaphone('a',1::bigint)", "metaphone", "unknown, bigint"},
				{"SELECT metaphone('a',1.5)", "metaphone", "unknown, numeric"},
				{"SELECT metaphone(source=>'a',max_output_length=>4)", "metaphone", "source => unknown, max_output_length => integer"},
				{"SELECT metaphone('a',max_output_length=>4)", "metaphone", "unknown, max_output_length => integer"},
				{"SELECT dmetaphone()", "dmetaphone", ""},
				{"SELECT dmetaphone(1)", "dmetaphone", "integer"},
				{"SELECT dmetaphone('a',4)", "dmetaphone", "unknown, integer"},
				{"SELECT dmetaphone(source=>'a')", "dmetaphone", "source => unknown"},
				{"SELECT dmetaphone_alt()", "dmetaphone_alt", ""},
				{"SELECT dmetaphone_alt(1)", "dmetaphone_alt", "integer"},
				{"SELECT dmetaphone_alt('a',4)", "dmetaphone_alt", "unknown, integer"},
				{"SELECT dmetaphone_alt(source=>'a')", "dmetaphone_alt", "source => unknown"},
				{`SELECT "Metaphone"('a',4)`, "Metaphone", "unknown, integer"},
				{`SELECT "Dmetaphone"('a')`, "Dmetaphone", "unknown"},
				{`SELECT "Dmetaphone_alt"('a')`, "Dmetaphone_alt", "unknown"},
				{"SELECT public.metaphone('a',max_output_length=>4)", "public.metaphone", "unknown, max_output_length => integer"},
				{"SELECT public.dmetaphone(source=>'a')", "public.dmetaphone", "source => unknown"},
				{"SELECT public.dmetaphone_alt(source=>'a')", "public.dmetaphone_alt", "source => unknown"},
				{"SELECT metaphone('a',VARIADIC ARRAY[4])", "metaphone", "unknown, integer[]"},
				{"SELECT dmetaphone(VARIADIC ARRAY['a'])", "dmetaphone", "text[]"},
				{"SELECT dmetaphone_alt(VARIADIC ARRAY['a'])", "dmetaphone_alt", "text[]"},
			} {
				fail(extensionMissing(tc.q, tc.name, tc.types))
			}
			q := "SELECT metaphone(source=>'a',4)"
			fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: fixture.Position(strings.LastIndex(q, "4") + 1)}))
			q = "SELECT metaphone(source=>'a',source=>'b')"
			fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `argument name "source" used more than once`, Position: fixture.Position(strings.LastIndex(q, "source") + 1)}))
			fail(fixture.ImplicitError("SELECT phon_missing.metaphone('a',4)", fixture.Diagnostic{Code: "3F000", Message: `schema "phon_missing" does not exist`, Position: "8"}))
			for _, name := range []string{"metaphone", "dmetaphone", "dmetaphone_alt", "public.metaphone", "public.dmetaphone", "public.dmetaphone_alt"} {
				args := "'a'"
				if strings.HasSuffix(name, ".metaphone") || name == "metaphone" {
					args += ",4"
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
			for _, q := range []string{"SELECT metaphone('gumbo','4')", "SELECT metaphone('gumbo',4::smallint)", "SELECT metaphone(ALL 'gumbo',4)", "SELECT metaphone('gumbo'::varchar,4)", "SELECT metaphone('gumbo'::char(5),4)"} {
				run(metaphoneSelect(q, "metaphone", "KM"))
				run(fixture.Prepared(metaphoneSelect(q, "metaphone", "KM")))
			}
			run(fixture.Exec("CREATE DOMAIN phon_text AS text", 0))
			run(metaphoneSelect("SELECT metaphone('gumbo'::phon_text,4)", "metaphone", "KM"))
			for _, tc := range []struct{ value, code, message string }{
				{"bad", "22P02", `invalid input syntax for type integer: "bad"`},
				{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
			} {
				q := "SELECT metaphone('a','" + tc.value + "')"
				fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(strings.LastIndex(q, "'"+tc.value+"'") + 1)}))
				s := fixture.ImplicitError("SELECT metaphone($1,$2)", fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $2 = '...'"}, "a", tc.value)
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				run(fixture.Prepared(s))
			}
			run(fixture.Exec("SET search_path TO pg_catalog", 0))
			for _, tc := range []struct{ name, args, types, want string }{
				{"metaphone", "'gumbo',4", "unknown, integer", "KM"}, {"dmetaphone", "'gumbo'", "unknown", "KMP"}, {"dmetaphone_alt", "'gumbo'", "unknown", "KMP"},
			} {
				fail(extensionMissing("SELECT "+tc.name+"("+tc.args+")", tc.name, tc.types))
				run(metaphoneSelect("SELECT public."+tc.name+"("+tc.args+")", tc.name, tc.want))
			}
			run(fixture.Exec("RESET search_path", 0))
		})
	})
	t.Run("PrivateSchema", func(t *testing.T) {
		metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA phon_addon", 0))
			run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA phon_addon", 0))
			run(fixture.Exec("CREATE USER phon_reader NOSUPERUSER", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION phon_reader", 0))
			for _, tc := range []struct{ name, args, want string }{{"metaphone", "'gumbo',4", "KM"}, {"dmetaphone", "'gumbo'", "KMP"}, {"dmetaphone_alt", "'gumbo'", "KMP"}} {
				s := fixture.ImplicitError("SELECT phon_addon."+tc.name+"("+tc.args+")", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema phon_addon", Position: "8"})
				s.SelectRows, s.ErrorStage = true, "execute"
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA phon_addon TO phon_reader", 0))
			} else {
				run(fixture.Exec("ALTER SCHEMA phon_addon OWNER TO phon_reader", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION phon_reader", 0))
			run(fixture.Select("SELECT phon_addon.metaphone('gumbo',4),phon_addon.dmetaphone('Smith'),phon_addon.dmetaphone_alt('Smith')", []string{"metaphone", "dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"KM", "SM0", "XMT"}}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	})
}
