package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextComparisonValues(t *testing.T) {
	citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE citext_compare_values(id integer,a citext,b citext)", 0))
		// Fixed vectors are independent of either backend's comparison result.
		for i, tc := range []struct {
			a, b any
			want []any
		}{
			{"ABC", "abc", []any{true, false, false, true, false, true}},
			{"aBc", "AbC", []any{true, false, false, true, false, true}},
			{"", "", []any{true, false, false, true, false, true}},
			{"ABC", "abd", []any{false, true, true, true, false, false}},
			{"abd", "ABC", []any{false, true, false, false, true, true}},
			{"", " ", []any{false, true, true, true, false, false}},
			{" ", "", []any{false, true, false, false, true, true}},
			{"abc", "abcd", []any{false, true, true, true, false, false}},
			{"abcd", "abc", []any{false, true, false, false, true, true}},
			{"abc", "abc ", []any{false, true, true, true, false, false}},
			{"abc ", "abc", []any{false, true, false, false, true, true}},
			{"É", "é", []any{false, true, true, true, false, false}},
			{"Σ", "σ", []any{false, true, true, true, false, false}},
			{"İ", "i", []any{false, true, false, false, true, true}},
			{"Straße", "STRASSE", []any{false, true, false, false, true, true}},
			{"é", "e\u0301", []any{false, true, false, false, true, true}},
			{nil, "abc", []any{nil, nil, nil, nil, nil, nil}},
			{"ABC", nil, []any{nil, nil, nil, nil, nil, nil}},
			{nil, nil, []any{nil, nil, nil, nil, nil, nil}},
		} {
			id := int64(i + 1)
			run(fixture.Exec("INSERT INTO citext_compare_values VALUES ($1,$2,$3)", 1, id, tc.a, tc.b))
			q := "SELECT a::text AS a,b::text AS b," + citextComparisonProjection("a", "b") + " FROM citext_compare_values WHERE id=$1"
			want := append([]any{tc.a, tc.b}, tc.want...)
			citextComparisonRoutes(t, p, b, c, fixture.Select(q, append([]string{"a", "b"}, citextComparisonNames...), append([]string{"TEXT", "TEXT"}, citextComparisonTypes...), [][]any{want}, id))
		}
		citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT id,a::text AS a FROM citext_compare_values WHERE a='abc' ORDER BY id", []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "ABC"}, {int64(2), "aBc"}, {int64(4), "ABC"}, {int64(8), "abc"}, {int64(10), "abc"}, {int64(18), "ABC"}}))
	})
}
