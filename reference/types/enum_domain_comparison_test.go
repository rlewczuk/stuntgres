package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Full diagnostics, stages and explicit-cast results independently captured in
// root tests-11 (artifact://35), using pinned PostgreSQL 18.0.
func TestReferenceEnumDomainComparison(t *testing.T) {
	fixture.EnumReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, query := range []string{
			`CREATE SCHEMA IF NOT EXISTS enum_saved`,
			`CREATE TYPE enum_saved."Odd Type" AS ENUM (' z ','','O''K','back\slash')`,
			`CREATE DOMAIN enum_saved.wrapped AS enum_saved."Odd Type"`,
			`CREATE DOMAIN enum_saved.other_wrapped AS enum_saved."Odd Type"`,
			`CREATE TABLE enum_saved.comparison(v enum_saved.wrapped,w enum_saved.wrapped,b enum_saved."Odd Type",d enum_saved.other_wrapped)`,
			`INSERT INTO enum_saved.comparison VALUES ('O''K','O''K','O''K','O''K')`,
		} {
			affected := int64(0)
			if query == `INSERT INTO enum_saved.comparison VALUES ('O''K','O''K','O''K','O''K')` {
				affected = 1
			}
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, affected))
		}
		defer func() {
			if _, err := conn.ExecContext(p.Ctx, "DROP SCHEMA enum_saved CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		for _, operator := range []string{"=", "<>", "<", "<=", ">", ">="} {
			for _, pair := range []struct {
				left, right, leftType, rightType string
			}{
				{"v", "b", "enum_saved.wrapped", `enum_saved."Odd Type"`},
				{"b", "v", `enum_saved."Odd Type"`, "enum_saved.wrapped"},
				{"v", "w", "enum_saved.wrapped", "enum_saved.wrapped"},
				{"v", "d", "enum_saved.wrapped", "enum_saved.other_wrapped"},
			} {
				query := "SELECT " + pair.left + " " + operator + " " + pair.right + " AS matched FROM enum_saved.comparison"
				want := fixture.Diagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883",
					Message:  "operator does not exist: " + pair.leftType + " " + operator + " " + pair.rightType,
					Hint:     "No operator matches the given name and argument types. You might need to add explicit type casts.",
					Position: "10",
				}
				t.Run(query, func(t *testing.T) {
					fixture.EnumReferenceErrorChecked(t, p, conn, query, true, "prepare", func(t *testing.T, got fixture.Diagnostic) {
						if got != want {
							t.Fatalf("diagnostic got %#v want %#v", got, want)
						}
					})
				})
			}
			// Independently captured equal-label results: true for =, <=, >=;
			// false for <>, <, >. Domain storage itself remains valid.
			want := operator == "=" || operator == "<=" || operator == ">="
			query := `SELECT v::enum_saved."Odd Type" ` + operator + " b AS matched FROM enum_saved.comparison"
			for _, prepared := range []bool{false, true} {
				step := fixture.Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{want}})
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
		}
	})
}
