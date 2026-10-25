package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSoundexBoundaries(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		for _, tc := range []struct{ input, code string }{
			{"B", "B000"}, {"BB", "B000"}, {"B-b", "B100"}, {"BHB", "B100"}, {"BWB", "B100"}, {"B1B", "B000"}, {"BéB", "B100"},
			// The preceding raw digit can equal the current letter's mapped code.
			{"B2B", "B100"}, {"D3D", "D000"},
			{"", ""}, {"123!?", ""}, {"東京🙂", ""}, {"é", ""}, {"123...anne", "A500"}, {"Émile", "M400"}, {"Emile", "E540"},
			{strings.Repeat("a", 255), "A000"}, {strings.Repeat("a", 256), "A000"},
			{strings.Repeat("!", 65536) + "Ann", "A500"}, {strings.Repeat("é", 32768), ""},
			{"A" + strings.Repeat("a!", 32768), "A000"}, {"Robert" + strings.Repeat("!", 65536), "R163"},
		} {
			soundexFrames(t, p, b, c, "soundex", []struct { args []any; want any }{{[]any{tc.input}, tc.code}})
		}
		// Each sequence is exercised on the very same held statement as well as
		// literal, direct bound and fresh-prepared public routes.
		soundexFrames(t, p, b, c, "soundex", []struct { args []any; want any }{
			{[]any{"Ann"}, "A500"}, {[]any{nil}, nil}, {[]any{""}, ""}, {[]any{"Anne"}, "A500"},
		})
		soundexFrames(t, p, b, c, "difference", []struct { args []any; want any }{
			{[]any{"Ann", "Anne"}, int64(4)}, {[]any{nil, "Ann"}, nil}, {[]any{"Ann", nil}, nil}, {[]any{nil, nil}, nil},
			{[]any{"", ""}, int64(4)}, {[]any{"", "!?"}, int64(4)}, {[]any{"", "A"}, int64(0)}, {[]any{"A", "B"}, int64(3)}, {[]any{"Anne", "Andrew"}, int64(2)},
		})
		for _, tc := range []struct { name string; args []any }{{"soundex", []any{"Ann"}}, {"difference", []any{"Ann", "Anne"}}} {
			s := soundexSelect(b, levenshteinSQL(tc.name, false, tc.args)+" WHERE false", tc.name, nil, tc.args...)
			s.Rows = nil
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE TABLE sx_demand(src text,z int)", 0))
		run(fixture.Exec("INSERT INTO sx_demand VALUES(NULL,0)", 1))
		s := fixture.ImplicitError("SELECT difference(src,(1/z)::text) FROM sx_demand", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Prepared(s))
		// Literal NULL does not protect the constant sibling from division by zero.
		s = fixture.ImplicitError("SELECT difference(NULL::text,(1/0)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Prepared(s))
	})
}
