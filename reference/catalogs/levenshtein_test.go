package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLevenshteinDistances(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.2'", 0))
		levenshteinFrames(t, p, b, c, "levenshtein", []struct {
			args []any
			want any
		}{
			{[]any{"kitten", "sitting"}, int64(3)},
			{[]any{"GUMBO", "GAMBOL"}, int64(2)},
			{[]any{"ordinary", "ordinary"}, int64(0)},
			{[]any{"", ""}, int64(0)},
			{[]any{"", "abc"}, int64(3)},
			{[]any{"abc", ""}, int64(3)},
			{[]any{"ab", "ba"}, int64(2)},
			{[]any{nil, "sitting"}, nil},
			{[]any{"kitten", "sitting"}, int64(3)},
		})
	})
}

func TestReferenceLevenshteinCosts(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		levenshteinFrames(t, p, b, c, "levenshtein", []struct {
			args []any
			want any
		}{
			{[]any{"GUMBO", "GAMBOL", 2, 1, 1}, int64(3)},
			{[]any{"a", "b", 2, 3, 7}, int64(5)},
			{[]any{"abc", "adc", 2, 3, 4}, int64(4)},
			{[]any{"", "abc", 2, 3, 7}, int64(6)},
			{[]any{"abc", "", 2, 3, 7}, int64(9)},
			{[]any{"abc", "xyz", 0, 0, 0}, int64(0)},
			{[]any{"a", "b", 1, 1, -4}, int64(-4)},
			{[]any{"a", "a", -1, -1, 1}, int64(-2)},
			// PostgreSQL 18.6 computes in int64 and rejects a distance outside
			// int4 (18.0 wrapped it).
			{[]any{"", "aa", 2147483647, 1, 1}, levenshteinOutOfRange},
			{[]any{"aa", "", 1, 2147483647, 1}, levenshteinOutOfRange},
			{[]any{"a", "b", 2147483647, 1, 1}, int64(1)},
			{[]any{"a", "aa", 1, -10, 1}, int64(-8)},
		})
	})
}

func TestReferenceLevenshteinBounds(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
		levenshteinFrames(t, p, b, c, "levenshtein_less_equal", []struct {
			args []any
			want any
		}{
			{[]any{"kitten", "sitting", 3}, int64(3)},
			{[]any{"kitten", "sitting", 4}, int64(3)},
			{[]any{"same", "same", 0}, int64(0)},
			{[]any{"extensive", "exhaustive", 4}, int64(4)},
			{[]any{"kitten", "sitting", -1}, int64(3)},
			{[]any{"kitten", "sitting", -2147483648}, int64(3)},
			{[]any{"kitten", "sitting", 2147483647}, int64(3)},
		})
		for _, tc := range []struct {
			args  []any
			bound int64
		}{
			{[]any{"kitten", "sitting", 2}, 2},
			{[]any{"a", "b", 0}, 0},
			{[]any{"extensive", "exhaustive", 2}, 2},
			{[]any{"a", "b", 2, 3, 7, 4}, 4},
		} {
			levenshteinOutside(t, p, b, c, tc.args, tc.bound)
		}
		levenshteinFrames(t, p, b, c, "levenshtein_less_equal", []struct {
			args []any
			want any
		}{
			{[]any{"GUMBO", "GAMBOL", 2, 1, 1, 3}, int64(3)},
			{[]any{"a", "b", 2, 3, 7, 5}, int64(5)},
			{[]any{"abc", "adc", 2, 3, 4, 4}, int64(4)},
			{[]any{"", "abc", 2, 3, 7, 6}, int64(6)},
			{[]any{"abc", "", 2, 3, 7, 9}, int64(9)},
			{[]any{"abc", "xyz", 0, 0, 0, 0}, int64(0)},
			{[]any{"a", "aa", 1, -10, 1, -1}, int64(-8)},
			{[]any{"a", "aa", 1, -10, 1, 0}, int64(1)},
			{[]any{"kitten", "sitting", 1, 1, 1, 3}, int64(3)},
			{[]any{"kitten", "sitting", 1, nil, 1, 3}, nil},
		})
		t.Run("ReleaseBoundedInt64", func(t *testing.T) {
			// REL_18_6 int64 arithmetic: costs near the int4 limits no longer
			// wrap (18.0 did), so a zero bound reports bound+1, and only a final
			// distance outside int4 fails.
			levenshteinFrames(t, p, b, c, "levenshtein_less_equal", []struct {
				args []any
				want any
			}{
				{[]any{"a", "b", 2147483647, 1, 1, 0}, int64(1)},
				{[]any{"a", "aaa", 2147483647, 1, 1, 0}, int64(1)},
				{[]any{"aaa", "a", 1, 2147483647, 1, 0}, int64(1)},
				{[]any{"a", "b", -2147483648, -2147483647, 2, 0}, levenshteinOutOfRange},
				{[]any{"aa", "bb", 1073741824, 0, 1073741824, 0}, int64(1)},
				{[]any{"aa", "bb", 2147483647, -2147483646, 1, 0}, int64(1)},
				// PostgreSQL 18.6's regression case: a theoretical maximum of
				// 2*2^30 no longer wraps.
				{[]any{"aaa", "aaaaa", 1073741824, 0, 1073741824, 10}, int64(11)},
			})
		})
		q := "SELECT levenshtein_less_equal($1,$2,$3,$4,$5,$6)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, s := range []fixture.Step{
			levenshteinSelect(q, "levenshtein_less_equal", int64(5), "a", "b", 2, 3, 7, 5),
			levenshteinSelect(q, "levenshtein_less_equal", nil, nil, "b", 1, 1, 1, 0),
			levenshteinLengthError(q, strings.Repeat("é", 256), "b", 1, 1, 1, 0),
			levenshteinSelect(q, "levenshtein_less_equal", int64(3), "kitten", "sitting", 1, 1, 1, 3),
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}
