package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLevenshteinUnicodeAndLength(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		levenshteinFrames(t, p, b, c, "levenshtein", []struct {
			args []any
			want any
		}{
			{[]any{"é", "e"}, int64(1)},
			{[]any{"é", "e\u0301"}, int64(2)},
			{[]any{"🙂", "🙃"}, int64(1)},
			{[]any{"a", "A"}, int64(1)},
		})
		for _, signature := range []struct {
			name string
			tail []any
		}{
			{"levenshtein", nil}, {"levenshtein", []any{1, 1, 1}},
			{"levenshtein_less_equal", []any{0}}, {"levenshtein_less_equal", []any{1, 1, 1, 0}},
		} {
			for _, char := range []string{"a", "é"} {
				t.Run(fmt.Sprint(signature.name, len(signature.tail), char), func(t *testing.T) {
					short, long := strings.Repeat(char, 255), strings.Repeat(char, 256)
					var frames []struct {
						args []any
						want any
					}
					for _, tc := range []struct {
						src, dst string
						want     int64
					}{
						{short, short, 0}, {short, "", 255}, {"", short, 255},
						{long, "", 256}, {"", long, 256},
					} {
						args := append([]any{tc.src, tc.dst}, signature.tail...)
						frames = append(frames, struct {
							args []any
							want any
						}{args, tc.want})
					}
					levenshteinFrames(t, p, b, c, signature.name, frames)
					for _, pair := range [][2]string{{long, "b"}, {"b", long}, {long, long}} {
						args := append([]any{pair[0], pair[1]}, signature.tail...)
						q := levenshteinSQL(signature.name, false, args)
						s := levenshteinLengthError(q, args...)
						run(s)
						run(fixture.Prepared(s)) // Parameter description succeeds; execution fails.
						run(levenshteinLengthError(levenshteinSQL(signature.name, true, args)))
						// PostgreSQL prepares the literal expression before executor folding.
						run(fixture.Prepared(levenshteinLengthError(levenshteinSQL(signature.name, true, args))))
					}
				})
			}
		}
	})
}

func TestReferenceLevenshteinNullAndDemand(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		for _, signature := range []struct {
			name string
			args []any
		}{
			{"levenshtein", []any{"a", "b"}}, {"levenshtein", []any{"a", "b", 1, 1, 1}},
			{"levenshtein_less_equal", []any{"a", "b", 1}}, {"levenshtein_less_equal", []any{"a", "b", 1, 1, 1, 1}},
		} {
			var frames []struct {
				args []any
				want any
			}
			for i := range signature.args {
				args := append([]any(nil), signature.args...)
				args[i] = nil
				frames = append(frames, struct {
					args []any
					want any
				}{args, nil})
			}
			args := append([]any(nil), signature.args...)
			args[0], args[1] = nil, strings.Repeat("é", 256)
			frames = append(frames, struct {
				args []any
				want any
			}{args, nil})
			levenshteinFrames(t, p, b, c, signature.name, frames)
			s := levenshteinSelect(levenshteinSQL(signature.name, false, signature.args)+" WHERE false", signature.name, nil, signature.args...)
			s.Rows = nil
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("CREATE TABLE lev_demand(src text,z int)", 0))
		run(fixture.Exec("INSERT INTO lev_demand VALUES(NULL,0)", 1))
		for _, q := range []string{
			"SELECT levenshtein(src,(1/z)::text) FROM lev_demand",
			"SELECT levenshtein(src,'b',1,1,1/z) FROM lev_demand",
			"SELECT levenshtein_less_equal(src,'b',1/z) FROM lev_demand",
			"SELECT levenshtein_less_equal(src,'b',1,1,1,1/z) FROM lev_demand",
		} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
			run(fixture.Prepared(s))
		}
		q := "SELECT levenshtein($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			levenshteinSelect(q, "levenshtein", int64(3), "kitten", "sitting"),
			levenshteinSelect(q, "levenshtein", nil, nil, "sitting"),
			levenshteinLengthError(q, strings.Repeat("a", 256), "b"),
			levenshteinSelect(q, "levenshtein", int64(1), "a", "b"),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
