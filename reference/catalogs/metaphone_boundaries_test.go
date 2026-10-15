package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMetaphoneBoundaries(t *testing.T) {
	metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		metaphoneFrames(t, p, b, c, "metaphone", []struct {
			args []any
			want any
		}{
			{[]any{"", 0}, ""}, {[]any{"", -1}, ""}, {[]any{"", 256}, ""}, {[]any{"", 2147483647}, ""},
			{[]any{"", nil}, nil}, {[]any{nil, 0}, nil}, {[]any{nil, 256}, nil},
			{[]any{strings.Repeat("a", 256), nil}, nil}, {[]any{strings.Repeat("a", 255), 255}, "A"},
			{[]any{strings.Repeat("é", 127), 4}, ""}, {[]any{"AX", 1}, "A"}, {[]any{"AX", 2}, "AK"}, {[]any{"AX", 3}, "AKS"},
		})
		q := "SELECT metaphone($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			source        string
			cap           int
			code, message string
		}{
			{strings.Repeat("a", 256), 0, "22023", "argument exceeds the maximum length of 255 bytes"},
			{strings.Repeat("a", 256), 256, "22023", "argument exceeds the maximum length of 255 bytes"},
			{strings.Repeat("é", 128), 4, "22023", "argument exceeds the maximum length of 255 bytes"},
			{"a", 256, "22023", "output exceeds the maximum length of 255 bytes"},
			{"!", 2147483647, "22023", "output exceeds the maximum length of 255 bytes"},
			{"a", 0, "2200F", "output cannot be empty string"},
			{"a", -2147483648, "2200F", "output cannot be empty string"},
			{"!", -1, "2200F", "output cannot be empty string"},
		} {
			t.Run(tc.code+"_"+tc.source+"_"+levenshteinSQL("cap", true, []any{tc.cap}), func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				s := metaphoneFailure(q, tc.code, tc.message, tc.source, tc.cap)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, metaphoneSelect(q, "metaphone", "K", "gumbo", 1))
				// Immutable literal evaluation may occur at execution, not Prepare.
				run(metaphoneFailure(levenshteinSQL("metaphone", true, []any{tc.source, tc.cap}), tc.code, tc.message))
			})
		}
		for _, tc := range []struct{ source, want string }{{strings.Repeat("a", 300), "A"}, {strings.Repeat("b", 300), "PPPP"}} {
			for _, name := range []string{"dmetaphone", "dmetaphone_alt"} {
				s := metaphoneSelect("SELECT "+name+"($1)", name, tc.want, tc.source)
				run(s)
				run(fixture.Prepared(s))
			}
		}
		// STRICT does not bypass SQL argument input conversion.
		s := fixture.ImplicitError("SELECT metaphone($1,$2)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $2 = '...'"}, nil, "bad")
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Prepared(s))
	})
}
