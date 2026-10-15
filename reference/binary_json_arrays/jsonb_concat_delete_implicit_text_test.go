package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBConcatDeleteImplicitText(t *testing.T) {
	jsonbConcatDeleteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE DOMAIN jbcd_implicit_varchar AS varchar",
			"CREATE DOMAIN jbcd_implicit_bpchar AS character(3)",
		} {
			jsonTypeRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, typ := range []string{
			"varchar", "varchar(3)", "character(1)", "character(3)",
			"jbcd_implicit_varchar", "jbcd_implicit_bpchar",
		} {
			t.Run(typ, func(t *testing.T) {
				for _, tc := range []struct {
					q    string
					args []any
				}{
					{`SELECT '{"a":1}' - 'a'::` + typ + ` AS v`, nil},
					{`SELECT $1 - 'a'::` + typ + ` AS v`, []any{`{"a":1}`}},
					{`SELECT $1 - $2::` + typ + ` AS v`, []any{`{"a":1}`, "a"}},
				} {
					t.Run(tc.q, func(t *testing.T) {
						// BPCHAR padding must pass through the existing implicit
						// TEXT cast, rather than become part of the object key.
						s := jsonbConcatDeleteSelect(tc.q, `{}`, tc.args...)
						jsonTypeBoth(t, p, b, c, jsonbPathDeleteMetadata(s, b.PG))
					})
				}
			})
		}
		t.Run("numeric_route", func(t *testing.T) {
			for _, tc := range []struct {
				q    string
				args []any
			}{
				{`SELECT '3' - 1 AS v`, nil},
				{`SELECT $1 - 1 AS v`, []any{"3"}},
			} {
				jsonTypeBoth(t, p, b, c, fixture.Select(tc.q, []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}, tc.args...))
			}
		})
		t.Run("both_unknown_ambiguity", func(t *testing.T) {
			for _, tc := range []struct {
				q, position string
				args        []any
			}{
				{`SELECT '1' - '1'`, "12", nil},
				{`SELECT $1 - $2`, "11", []any{"1", "1"}},
			} {
				d := fixture.ControlsReferenceDiagnostic("42725", "operator is not unique: unknown - unknown")
				d.Position = tc.position
				d.Hint = "Could not choose a best candidate operator. You might need to add explicit type casts."
				jsonTypeAnalysisError(t, p, b, c, jsonbConcatDeleteError(tc.q, d, tc.args...))
			}
		})
	})
}
