package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func citextComparisonHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT " + citextComparisonProjection("$1::citext", "$2::citext")
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		a, b any
		want []any
	}{
		{"ABC", "abc", []any{true, false, false, true, false, true}},
		{"ABC", "abd", []any{false, true, true, true, false, false}},
		{"abd", "ABC", []any{false, true, false, false, true, true}},
		{nil, "abc", []any{nil, nil, nil, nil, nil, nil}},
		{"ABC", nil, []any{nil, nil, nil, nil, nil, nil}},
		{nil, nil, []any{nil, nil, nil, nil, nil, nil}},
		{"ABC", "abc", []any{true, false, false, true, false, true}},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, citextComparisonNames, citextComparisonTypes, [][]any{tc.want}, tc.a, tc.b))
	}
}

func TestReferenceCitextComparisonBinding(t *testing.T) {
	t.Run("coercion_and_held", func(t *testing.T) {
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			for _, tc := range []struct {
				expr string
				want bool
			}{
				{"'ABC'::citext = 'abc'", true}, {"'ABC' = 'abc'::citext", true},
				{"'ABC'::citext = 'abc'::text", false}, {"'ABC'::text = 'abc'::citext", false},
				{"'ABC'::citext = 'abc'::varchar", false}, {"'ABC'::varchar = 'abc'::citext", false},
				{"('ABC'::citext)::text = ('abc'::citext)::text", false}, {"'ABC' = 'abc'", false},
			} {
				citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT "+tc.expr+" AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{tc.want}}))
			}
			citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT citext_eq('ABC','abc') AS eq,citext_ne('ABC','abc') AS ne,citext_lt('ABC','abc') AS lt,citext_le('ABC','abc') AS le,citext_gt('ABC','abc') AS gt,citext_ge('ABC','abc') AS ge", citextComparisonNames, citextComparisonTypes, [][]any{{true, false, false, true, false, true}}))
			for _, tc := range []struct {
				a, b any
				want []any
			}{
				{"ABC", "abd", []any{false, true, true, true, false, false}},
				{"abd", "ABC", []any{false, true, false, false, true, true}},
				{nil, "abc", []any{nil, nil, nil, nil, nil, nil}},
				{"ABC", nil, []any{nil, nil, nil, nil, nil, nil}},
				{nil, nil, []any{nil, nil, nil, nil, nil, nil}},
			} {
				q := "SELECT citext_eq($1::citext,$2::citext) AS eq,citext_ne($1::citext,$2::citext) AS ne,citext_lt($1::citext,$2::citext) AS lt,citext_le($1::citext,$2::citext) AS le,citext_gt($1::citext,$2::citext) AS gt,citext_ge($1::citext,$2::citext) AS ge"
				citextComparisonRoutes(t, p, b, c, fixture.Select(q, citextComparisonNames, citextComparisonTypes, [][]any{tc.want}, tc.a, tc.b))
			}
			citextComparisonHeld(t, p, b, c)
		})
	})
	t.Run("qualified_visibility", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "citext_compare_schema")
		citextComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA citext_compare_schema", 0))
			run(fixture.Exec("CREATE EXTENSION citext SCHEMA citext_compare_schema", 0))
			run(fixture.Exec("SET search_path TO pg_catalog,public", 0))
			citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT 'ABC'::citext_compare_schema.citext = 'abc'::citext_compare_schema.citext AS fallback,'ABC'::citext_compare_schema.citext OPERATOR(citext_compare_schema.=) 'abc'::citext_compare_schema.citext AS qualified", []string{"fallback", "qualified"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}))
			run(fixture.Exec("SET search_path TO citext_compare_schema,public,pg_catalog", 0))
			citextComparisonRoutes(t, p, b, c, fixture.Select("SELECT 'ABC'::citext = 'abc'::citext AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
		})
	})
}
