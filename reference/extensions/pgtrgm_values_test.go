package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgTrgmTrigrams(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		q := "SELECT show_trgm($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, tc := range []struct {
			input    any
			trigrams []string
		}{
			{"cat", []string{"  c", " ca", "at ", "cat"}},
			{"CAT", []string{"  c", " ca", "at ", "cat"}},
			{"cat cat", []string{"  c", " ca", "at ", "cat"}},
			{"a", []string{"  a", " a "}},
			{"aaaa", []string{"  a", " aa", "aa ", "aaa"}},
			{"foo|bar", []string{"  b", "  f", " ba", " fo", "ar ", "bar", "foo", "oo "}},
			{"", nil}, {"   ", nil}, {"!!!", nil}, {"é", nil}, {"雪", nil}, {"🙂", nil},
			{"café", []string{"  c", " ca", "af ", "caf"}},
			{"AÉB", []string{"  a", "  b", " a ", " b "}},
			{"cafe\u0301", []string{"  c", " ca", "afe", "caf", "fe "}},
			{"12", []string{"  1", " 12", "12 "}},
			{"a_b", []string{"  a", "  b", " a ", " b "}},
			{"a-b", []string{"  a", "  b", " a ", " b "}},
			{"a\nb", []string{"  a", "  b", " a ", " b "}},
			{"a...b", []string{"  a", "  b", " a ", " b "}},
			{nil, nil},
		} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				run := pgTrgmRun(t, p, b, c)
				var want any = pgTrgmArray(tc.trigrams...)
				literal := "NULL::text"
				if tc.input == nil {
					want = nil
				} else {
					literal = pgTrgmLiteral(tc.input.(string))
				}
				s := pgTrgmScalar(b, q, "show_trgm", "_TEXT", want, tc.input)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(pgTrgmScalar(b, "SELECT show_trgm("+literal+")", "show_trgm", "_TEXT", want))
			})
		}
		// An owned []byte text bind must not be retained as borrowed caller data.
		bytes := []byte("cat")
		run(pgTrgmScalar(b, q, "show_trgm", "_TEXT", pgTrgmArray("  c", " ca", "at ", "cat"), bytes))
		copy(bytes, "dog")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTrgmScalar(b, q, "show_trgm", "_TEXT", pgTrgmArray("  d", " do", "dog", "og "), bytes))
		run(fixture.Select(
			"SELECT cardinality(show_trgm('')),array_ndims(show_trgm('')),array_lower(show_trgm(''),1),array_upper(show_trgm(''),1),array_lower(show_trgm('cat'),1),array_upper(show_trgm('cat'),1)",
			[]string{"cardinality", "array_ndims", "array_lower", "array_upper", "array_lower", "array_upper"},
			[]string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4"},
			[][]any{{int64(0), nil, nil, nil, int64(1), int64(4)}},
		))
		run(pgTrgmSelect(b, "SELECT show_trgm(v) FROM (SELECT 'cat'::text AS v) s WHERE false", []string{"show_trgm"}, []string{"_TEXT"}, nil))
		pgTrgmNotices(t, notices)
	})
}

func TestReferencePgTrgmScores(t *testing.T) {
	pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		run := pgTrgmRun(t, p, b, c)
		run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
		q := "SELECT similarity($1,$2),word_similarity($1,$2),strict_word_similarity($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, tc := range []struct {
			left, right string
			ratios      [3][2]int
		}{
			{"cat", "cat cat", [3][2]int{{1, 1}, {1, 1}, {1, 1}}},
			{"aaa", "aaaa", [3][2]int{{1, 1}, {1, 1}, {1, 1}}},
			{"a", "aa", [3][2]int{{1, 4}, {1, 2}, {1, 4}}},
			{"aa", "a", [3][2]int{{1, 4}, {1, 3}, {1, 4}}},
			{"word", "two words", [3][2]int{{4, 11}, {4, 5}, {4, 7}}},
			{"word", "two word", [3][2]int{{5, 9}, {1, 1}, {1, 1}}},
			{"two words", "word", [3][2]int{{4, 11}, {2, 5}, {4, 11}}},
			{"two words", "x two words y", [3][2]int{{5, 7}, {1, 1}, {1, 1}}},
			{"cat", "x cat cat y", [3][2]int{{1, 2}, {1, 1}, {1, 1}}},
			{"", "cat", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"cat", "", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"", "", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"!!!", "cat", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"cat", "!!!", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"é", "é", [3][2]int{{0, 1}, {0, 1}, {0, 1}}},
			{"café", "caf", [3][2]int{{1, 1}, {1, 1}, {1, 1}}},
			{"CAT!!!", "cat", [3][2]int{{1, 1}, {1, 1}, {1, 1}}},
			{"a_b", "a b", [3][2]int{{1, 1}, {1, 1}, {1, 1}}},
			{"café", "cafe\u0301", [3][2]int{{1, 2}, {3, 4}, {1, 2}}},
		} {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				run := pgTrgmRun(t, p, b, c)
				want := make([]any, 3)
				for j, r := range tc.ratios {
					want[j] = pgTrgmReal(b.PG, r[0], r[1])
				}
				names := []string{"similarity", "word_similarity", "strict_word_similarity"}
				types := []string{"FLOAT4", "FLOAT4", "FLOAT4"}
				s := pgTrgmSelect(b, q, names, types, [][]any{want}, tc.left, tc.right)
				run(s)
				run(fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				literal := "SELECT similarity(" + pgTrgmLiteral(tc.left) + "," + pgTrgmLiteral(tc.right) + "),word_similarity(" + pgTrgmLiteral(tc.left) + "," + pgTrgmLiteral(tc.right) + "),strict_word_similarity(" + pgTrgmLiteral(tc.left) + "," + pgTrgmLiteral(tc.right) + ")"
				run(pgTrgmSelect(b, literal, names, types, [][]any{want}))
			})
		}
		for _, args := range [][]any{{nil, "cat"}, {"cat", nil}, {nil, nil}} {
			s := pgTrgmSelect(b, q, []string{"similarity", "word_similarity", "strict_word_similarity"}, []string{"FLOAT4", "FLOAT4", "FLOAT4"}, [][]any{{nil, nil, nil}}, args...)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		pgTrgmNotices(t, notices)
	})
}
