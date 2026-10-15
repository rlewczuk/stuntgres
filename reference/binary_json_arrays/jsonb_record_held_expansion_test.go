package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordHeldExpansion(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_star_hold AS (a int,b text)`)
		call := `jsonb_populate_record(NULL::jbr_star_hold,$1::jsonb)`
		type heldCase struct {
			name, query     string
			columns, types  []string
			repeated, mixed bool
			statement       *sql.Stmt
		}
		cases := []heldCase{
			{name: "plain", query: `SELECT * FROM ` + call, columns: []string{"a", "b"}, types: []string{"INT4", "TEXT"}},
			{name: "qualified", query: `SELECT r.* FROM ` + call + ` r`, columns: []string{"a", "b"}, types: []string{"INT4", "TEXT"}},
			{name: "aliases", query: `SELECT r.* FROM ` + call + ` AS r(x,y)`, columns: []string{"x", "y"}, types: []string{"INT4", "TEXT"}},
			{name: "mixed", query: `SELECT * FROM ROWS FROM(` + call + `,generate_series(7,7)) WITH ORDINALITY`, columns: []string{"a", "b", "generate_series", "ordinality"}, types: []string{"INT4", "TEXT", "INT4", "INT8"}, mixed: true},
			{name: "repeated", query: `SELECT r.*,r.* FROM ` + call + ` r`, columns: []string{"a", "b", "a", "b"}, types: []string{"INT4", "TEXT", "INT4", "TEXT"}, repeated: true},
			{name: "nested", query: `SELECT * FROM (SELECT r.* FROM ` + call + ` r) s`, columns: []string{"a", "b"}, types: []string{"INT4", "TEXT"}},
		}
		for i := range cases {
			stmt, err := c.PrepareContext(p.Ctx, cases[i].query)
			if err != nil {
				t.Fatal(cases[i].name, err)
			}
			cases[i].statement = stmt
			defer stmt.Close()
		}
		document := `{"a":2,"b":"retained","label":"renamed","c":3}`
		observe := func(phase string, value any) {
			for _, tc := range cases {
				t.Run(phase+"/"+tc.name, func(t *testing.T) {
					row := []any{int64(2), value}
					if tc.repeated {
						row = append(row, int64(2), value)
					}
					if tc.mixed {
						row = append(row, int64(7), int64(1))
					}
					rows, err := tc.statement.QueryContext(p.Ctx, document)
					fixture.StringCoreRetainedRows(t, rows, err, tc.columns, tc.types, [][]any{row}, false)
				})
			}
		}
		observe("original", "retained")
		jsonbRecordSetup(t, p, b, c, `ALTER TYPE jbr_star_hold ADD ATTRIBUTE c int`)
		observe("add", "retained")
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM `+call, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(2), "retained", int64(3)}}, document))
		// Even invisible added members must still run their destination codec.
		for _, tc := range cases {
			t.Run("added member conversion/"+tc.name, func(t *testing.T) {
				rows, err := tc.statement.QueryContext(p.Ctx, `{"a":2,"b":"retained","c":"bad"}`)
				if rows != nil {
					rows.Close()
					t.Error("failed conversion exposed cursor")
				}
				fixture.EnumReferenceAssertDiagnostic(t, err, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`))
			})
		}
		jsonbRecordSetup(t, p, b, c, `ALTER TYPE jbr_star_hold RENAME ATTRIBUTE b TO label`)
		observe("rename", "renamed")
		jsonbRecordSetup(t, p, b, c, `ALTER TYPE jbr_star_hold DROP ATTRIBUTE label`)
		for _, tc := range cases {
			t.Run("drop/"+tc.name, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42703", "attribute 2 of type record has been dropped")
				t.Run("query", func(t *testing.T) {
					rows, err := tc.statement.QueryContext(p.Ctx, document)
					if rows != nil {
						rows.Close()
						t.Error("dropped attribute exposed cursor")
					}
					fixture.EnumReferenceAssertDiagnostic(t, err, want)
				})
				t.Run("exec", func(t *testing.T) {
					result, err := tc.statement.ExecContext(p.Ctx, document)
					if result != nil {
						t.Error("dropped attribute exposed result")
					}
					fixture.EnumReferenceAssertDiagnostic(t, err, want)
				})
			})
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM `+call, []string{"a", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(3)}}, document))
	})
}
