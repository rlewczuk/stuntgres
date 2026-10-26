package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// REL_18_6 parse_coerce.c unknown Const input, parse_param.c variable Params,
// and parse_func.c typename shortcut: a Param is never an unknown Const.
func TestReferenceCastImplementedUnknownParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, form := range []string{"'x'::unknown", "CAST('x' AS unknown)"} {
				s := castResultStep(t, "SELECT "+form+" AS v", castInventoryType("text"), "x", b.PG)
				run(s)
				run(fixture.Prepared(s))
			}
			castWitnessRoutes(t, p, b,
				castResultStep(t, "SELECT $1::unknown AS v", castInventoryType("text"), "x", b.PG, "x"),
				castResultStep(t, "SELECT $1::unknown AS v", castInventoryType("text"), "y", b.PG, "y"),
				castResultStep(t, "SELECT $1::unknown AS v", castInventoryType("text"), nil, b.PG, nil))
			for _, tc := range []struct {
				query      string
				diagnostic fixture.Diagnostic
			}{
				{"SELECT 1::unknown AS v", fixture.Diagnostic{Code: "42846", Message: "cannot cast type integer to unknown", Position: "9"}},
				{"SELECT unknown(1) AS v", fixture.Diagnostic{Code: "42883", Message: "function unknown(integer) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}},
			} {
				s := fixture.ImplicitError(tc.query, tc.diagnostic)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				s.ErrorStage = "prepare"
				run(fixture.Prepared(s))
			}
			for _, q := range []string{
				"SELECT CAST('00000000000000000000000000000001' AS uuid) AS v",
				"SELECT '00000000000000000000000000000001'::uuid AS v",
				"SELECT uuid('00000000000000000000000000000001') AS v",
			} {
				step := castResultStep(t, q, castInventoryType("uuid"), "00000000-0000-0000-0000-000000000001", b.PG)
				run(step)
				run(fixture.Prepared(step))
			}
			for _, form := range []string{"CAST($1 AS uuid)", "$1::uuid"} {
				q := "SELECT " + form + " AS v"
				castWitnessRoutes(t, p, b,
					castResultStep(t, q, castInventoryType("uuid"), "00000000-0000-0000-0000-000000000001", b.PG, "00000000000000000000000000000001"),
					castResultStep(t, q, castInventoryType("uuid"), "00000000-0000-0000-0000-000000000002", b.PG, "{00000000-0000-0000-0000-000000000002}"),
					castResultStep(t, q, castInventoryType("uuid"), nil, b.PG, nil))
			}
			// Unknown NULL can adopt UUID; a typed NULL UUID cannot become INTEGER.
			for _, q := range []string{"SELECT CAST(NULL AS uuid) AS v", "SELECT NULL::uuid AS v", "SELECT uuid(NULL) AS v"} {
				s := castResultStep(t, q, castInventoryType("uuid"), nil, b.PG)
				run(s)
				run(fixture.Prepared(s))
			}
			q := "SELECT CAST(NULL::uuid AS integer) AS v"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42846", Message: "cannot cast type uuid to integer", Position: "8"})
			// Type syntax fixes the parameter before its value is supplied. Bad input is
			// a codec failure, not an invented missing cast; its Bind context is owned by
			// the parameter importer, not by the cast expression.
			q = "SELECT CAST($1 AS uuid) AS v"
			want := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type uuid: "bad"`, Context: `unnamed portal parameter $1 = '...'`}
			bad := fixture.ImplicitError(q, want, "bad")
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			castWitnessRoutes(t, p, b, bad, castResultStep(t, q, castInventoryType("uuid"), nil, b.PG, nil), castResultStep(t, q, castInventoryType("uuid"), "00000000-0000-0000-0000-000000000001", b.PG, "00000000000000000000000000000001"))
			// $2's NULL test supplies no type. Neither a typed first parameter nor an
			// empty filter may conceal the unresolved-parameter analysis failure.
			for _, suffix := range []string{"", " AND false"} {
				q = "SELECT CAST($1 AS uuid) AS v WHERE $2 IS NULL" + suffix
				castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42P18", Message: "could not determine data type of parameter $2"}, nil, nil)
			}
			// The unknown-Const typename shortcut does not extend to unknown Params.
			q = "SELECT uuid($1) AS v"
			castWitnessDenied(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "function uuid(unknown) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"}, nil)
		})
	}
}
