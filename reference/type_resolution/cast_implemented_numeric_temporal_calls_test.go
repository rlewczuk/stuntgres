package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// A leading number is an ordinary function argument unless the complete
// production is a precision-qualified temporal literal. Exact routine lookup
// and missing-signature diagnostics must remain reachable in either case.
func TestReferenceCastImplementedNumericTemporalCalls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			run(fixture.Exec("SET DateStyle='ISO,MDY'", 0))
			missing := fixture.Diagnostic{
				Code: "42883", Message: "function date(integer) does not exist",
				Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8",
			}
			castWitnessDenied(t, p, b, "SELECT date(3)", missing)
			run(fixture.Exec("CREATE FUNCTION public.date(integer) RETURNS text LANGUAGE SQL AS $$SELECT 'date-exact'::text$$", 0))
			// The return type distinguishes the real callable from a typename cast.
			castWitnessRoutes(t, p, b, fixture.Select("SELECT date(3) AS v,public.date(1 + 2) AS qualified", []string{"v", "qualified"}, []string{"TEXT", "TEXT"}, [][]any{{"date-exact", "date-exact"}}))
			// Its argument starts with NUMBER but evaluates to DATE, not INTEGER;
			// exact date(date) / identity recognition must not select date(integer).
			castWitnessRoutes(t, p, b, castResultStep(t, "SELECT date(1 + DATE '2000-01-01') AS v", castInventoryType("date"), castExpectedValue(t, castInventoryType("date"), "2000-01-02", b.PG), b.PG))
			castWitnessRoutes(t, p, b, fixture.Select(
				"SELECT TIME(3) '12:34:56.1236'::text AS t,TIMESTAMP(3) '2000-01-01 12:34:56.1236'::text AS ts,TIMESTAMPTZ(3) '2000-01-01 12:34:56.1236+00'::text AS z",
				[]string{"t", "ts", "z"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"12:34:56.124", "2000-01-01 12:34:56.124", "2000-01-01 12:34:56.124+00"}}))
			// DATE has no typmod even when date(integer) exists. The trailing
			// literal therefore retains its type-modifier diagnostic and location.
			castWitnessDenied(t, p, b, "/*é*/ SELECT DATE(3) '2000-01-01'", fixture.Diagnostic{Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "14"})
			run(fixture.Exec("DROP FUNCTION public.date(integer)", 0))
			missing.Position = "14"
			castWitnessDenied(t, p, b, "/*é*/ SELECT date(3)", missing)
		})
	}
}
