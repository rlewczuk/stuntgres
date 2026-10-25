package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSoundexDiagnostics(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		fail := func(s fixture.Step) {
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			s.Prepared, s.ErrorStage = true, "prepare"
			run(s)
		}
		fail(extensionMissing("SELECT soundex('Ann')", "soundex", "unknown"))
		fail(extensionMissing("SELECT difference('Ann','Anne')", "difference", "unknown, unknown"))
		run(fixture.Exec("CREATE SCHEMA sx_addon", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch SCHEMA sx_addon", 0))
		run(fixture.Exec("SET search_path TO sx_addon,public", 0))
		for _, tc := range []struct{ q, name, types string }{
			{"SELECT soundex()", "soundex", ""},
			{"SELECT soundex(1)", "soundex", "integer"},
			{"SELECT soundex('a','b')", "soundex", "unknown, unknown"},
			{"SELECT soundex(source=>'a')", "soundex", "source => unknown"},
			{"SELECT soundex(VARIADIC ARRAY['a'])", "soundex", "text[]"},
			{"SELECT difference()", "difference", ""},
			{"SELECT difference('a')", "difference", "unknown"},
			{"SELECT difference('a','b','c')", "difference", "unknown, unknown, unknown"},
			{"SELECT difference(1,'a')", "difference", "integer, unknown"},
			{"SELECT difference('a',1)", "difference", "unknown, integer"},
			{"SELECT difference(source=>'a',target=>'b')", "difference", "source => unknown, target => unknown"},
			{"SELECT difference('a',target=>'b')", "difference", "unknown, target => unknown"},
			{"SELECT difference(VARIADIC ARRAY['a','b'])", "difference", "text[]"},
			{`SELECT "Soundex"('a')`, "Soundex", "unknown"},
			{`SELECT "Difference"('a','b')`, "Difference", "unknown, unknown"},
			{"SELECT sx_addon.soundex(source=>'a')", "sx_addon.soundex", "source => unknown"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := extensionMissing(tc.q, tc.name, tc.types)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				s.Prepared, s.ErrorStage = true, "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			})
		}
		q := "SELECT difference(source=>'a','b')"
		fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "positional argument cannot follow named argument", Position: fixture.Position(strings.LastIndex(q, "'b'") + 1)}))
		q = "SELECT difference(source=>'a',source=>'b')"
		fail(fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `argument name "source" used more than once`, Position: fixture.Position(strings.LastIndex(q, "source") + 1)}))
		fail(fixture.ImplicitError("SELECT sx_missing.soundex('a')", fixture.Diagnostic{Code: "3F000", Message: `schema "sx_missing" does not exist`, Position: "8"}))
		for _, name := range []string{"soundex", "difference", "sx_addon.soundex", "sx_addon.difference"} {
			args := "'Ann'"
			if strings.HasSuffix(name, "difference") {
				args += ",'Anne'"
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
		for _, tc := range []struct { q, name string; want any }{
			{"SELECT soundex(1::text)", "soundex", ""},
			{"SELECT difference(1::text,2::text)", "difference", int64(4)},
			{"SELECT soundex(ALL 'Ann'::varchar)", "soundex", "A500"},
		} {
			run(soundexSelect(b, tc.q, tc.name, tc.want))
			run(fixture.Prepared(soundexSelect(b, tc.q, tc.name, tc.want)))
		}
		run(fixture.Exec("SET search_path TO pg_catalog", 0))
		fail(extensionMissing("SELECT soundex('Ann')", "soundex", "unknown"))
		fail(extensionMissing("SELECT difference('Ann','Anne')", "difference", "unknown, unknown"))
		run(soundexSelect(b, "SELECT sx_addon.soundex('Ann')", "soundex", "A500"))
		run(soundexSelect(b, "SELECT sx_addon.difference('Ann','Anne')", "difference", int64(4)))
		run(fixture.Exec("RESET search_path", 0))
		run(fixture.Exec("CREATE USER sx_reader NOSUPERUSER", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION sx_reader", 0))
		for _, call := range []string{"soundex('Ann')", "difference('Ann','Anne')"} {
			fail(fixture.ImplicitError("SELECT sx_addon."+call, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema sx_addon", Position: "8"}))
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
