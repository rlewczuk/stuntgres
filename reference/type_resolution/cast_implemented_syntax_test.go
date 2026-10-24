package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// PG18 sql-expressions 4.2.9 and REL_18_6 parse_func.c: actual exact callable
// lookup precedes typename recognition; neither constitutes a declared CAST.
func TestReferenceCastImplementedSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE cast_syntax_source(u uuid,n integer,s text)", 0))
			run(fixture.Exec("INSERT INTO cast_syntax_source VALUES('00000000-0000-0000-0000-000000000001',42,'00000000000000000000000000000001'),(NULL,NULL,NULL)", 2))
			for _, form := range []string{"CAST(n AS integer)", "n::int4", "CAST(n AS pg_catalog.int4)", "(n)::pg_catalog.\"int4\"", "pg_catalog.int4(n)", "\"int4\"(n)"} {
				q := "SELECT " + form + " AS v FROM cast_syntax_source WHERE $1 ORDER BY n NULLS LAST"
				full := fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}, {nil}}, true)
				empty := full
				empty.Args = []any{false}
				empty.Rows = nil
				castWitnessRoutes(t, p, b, full, empty)
			}
			for _, form := range []string{"CAST(s AS uuid)", "s::pg_catalog.uuid", "pg_catalog.uuid(s)", "\"uuid\"(s)"} {
				q := "SELECT " + form + " AS v FROM cast_syntax_source ORDER BY n NULLS LAST"
				step := fixture.Select(q, []string{"v"}, []string{"UUID"}, [][]any{{"00000000-0000-0000-0000-000000000001"}, {nil}})
				castWitnessRoutes(t, p, b, step)
			}
			run(fixture.Exec("CREATE FUNCTION public.int4(uuid) RETURNS text LANGUAGE SQL AS $$SELECT 'exact-callable'::text$$", 0))
			for _, callee := range []string{"int4", "public.int4", "public.\"int4\""} {
				q := "SELECT " + callee + "(u) AS v FROM cast_syntax_source ORDER BY n NULLS LAST"
				// The actual non-STRICT routine returns TEXT even for NULL UUID. Replacing
				// it with a type coercion would give either wrong metadata or a NULL row.
				castWitnessRoutes(t, p, b, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"exact-callable"}, {"exact-callable"}}))
			}
			q := "SELECT pg_catalog.int4(u) AS v FROM cast_syntax_source"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "function pg_catalog.int4(uuid) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
			q = "SELECT CAST(u AS integer) AS v FROM cast_syntax_source"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type uuid to integer", Position: "8"})
			// Actual function call and typename shortcut are not a blanket array ban.
			// String IO and identity work; element-wise ARRAYCOERCE is not a shortcut.
			for _, q := range []string{"SELECT pg_catalog._int4(CAST($1 AS text)) AS v", "SELECT pg_catalog._int4(CAST($1 AS int4[])) AS v"} {
				castWitnessRoutes(t, p, b, castResultStep(t, q, castInventoryType("int4[]"), "{1,NULL}", b.PG, "{1,NULL}"), castResultStep(t, q, castInventoryType("int4[]"), nil, b.PG, nil))
			}
			q = "SELECT pg_catalog._int8(CAST($1 AS int4[])) AS v"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "function pg_catalog._int8(integer[]) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, "{1,NULL}")
			run(fixture.Exec("CREATE TYPE cast_syntax_enum AS ENUM('red','blue')", 0))
			for _, tc := range []struct {
				expression string
				fact       castImplementedType
				value      any
			}{
				{"(pg_sleep(0))::void", castInventoryType("void"), ""},
				{"(ROW(1,'red'::text))::record", castInventoryType("cast_anonymous_record"), "(1,red)"},
				{"(ARRAY['red'::cast_syntax_enum,NULL::cast_syntax_enum])::cast_syntax_enum[]", castImplementedType{name: "cast_syntax_enum[]", element: "cast_syntax_enum"}, "{red,NULL}"},
			} {
				run(castResultStep(t, "SELECT "+tc.expression+" AS v", tc.fact, tc.value, b.PG))
			}
		})
	}
}
