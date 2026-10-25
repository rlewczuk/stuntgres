package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryIntGrammar(t *testing.T) {
	// REL_18_0 regression strings and independently specified boundaries were
	// confirmed by the pinned PostgreSQL 18.0 oracle before implementation.
	cases := []struct{ input, canonical string }{
		{" 1 ", "1"}, {" ! 1 ", "!1"}, {"1|!2", "1 | !2"},
		{"!(!1|2)", "!( !1 | 2 )"}, {"(1&2)", "1 & 2"}, {"1&(2)", "1 & 2"},
		{"!(1)&2", "!1 & 2"}, {"!(1&2)", "!( 1 & 2 )"},
		{"1|2&3", "1 | 2 & 3"}, {"1|(2&3)", "1 | 2 & 3"}, {"(1|2)&3", "( 1 | 2 ) & 3"},
		{"1&(2|3)", "1 & ( 2 | 3 )"}, {"!!1", "!( !1 )"},
		{"1|2|3", "( 1 | 2 ) | 3"}, {"1|(2|3)", "1 | ( 2 | 3 )"},
		{"1&1", "1 & 1"}, {"3&2&1", "3 & 2 & 1"}, {"1|1", "1 | 1"},
		{"0", "0"}, {"-1", "-1"}, {"2147483647", "2147483647"}, {"-2147483648", "-2147483648"},
		{"010", "8"}, {"09", "0"}, {"018", "1"}, {"-", "0"}, {"-010", "-8"}, {"-0", "0"},
		{strings.Repeat("0", 15), "0"}, {"-" + strings.Repeat("0", 14), "0"},
		{strings.Repeat("(", 32) + "1" + strings.Repeat(")", 32), "1"},
		{strings.Repeat("1&", 63) + "1", strings.Repeat("1 & ", 63) + "1"},
	}
	nots := "!1"
	for i := 1; i < 16; i++ {
		nots = "!( " + nots + " )"
	}
	cases = append(cases, struct{ input, canonical string }{strings.Repeat("!", 16) + "1", nots})
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				queryIntInstall(t, p, b, c)
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE qi_grammar(id integer NOT NULL,v query_int)", 0))
				run(fixture.Exec("INSERT INTO qi_grammar VALUES (1,"+queryIntLiteral(tc.input)+")", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO qi_grammar VALUES ($1,$2)", 1, 2, tc.input)))
				s := fixture.QueryIntSelect(b, "SELECT id,v FROM qi_grammar ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), tc.canonical}, {int64(2), tc.canonical}})
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
	t.Run("flat_OR_not_limited_to_16", func(t *testing.T) {
		queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			queryIntInstall(t, p, b, c)
			for _, n := range []int{15, 16, 17, 32} {
				input := strings.Repeat("1|", n) + "1"
				canonical := "1 | 1"
				for i := 1; i < n; i++ {
					canonical = "( " + canonical + " ) | 1"
				}
				s := fixture.QueryIntSelect(b, "SELECT $1::query_int AS v", []string{"v"}, [][]any{{canonical}}, input)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
}
