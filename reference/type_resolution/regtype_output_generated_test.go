package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL validates explicit expression IO before adding the generated
// column's destination assignment: only the latter is admitted.
func TestReferenceRegtypeOutputAuthorityGenerated(t *testing.T) {
	fixture.RegtypeScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
		fixture.RegtypeSetup(t, p, conn, recorder)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE DOMAIN regtype_a.identifier AS regtype", 0))
		cases := []struct {
			name, expression string
			values           []any
		}{
			{"row", "r", []any{"integer", "same", nil}},
			{"domain_row", "d", []any{"integer", "same", nil}},
			{"builtin_literal", "'integer'::regtype", []any{"integer", "integer", "integer"}},
			{"user_literal", "'regtype_a.same'::regtype", []any{"same", "same", "same"}},
			{"domain_builtin_literal", "'integer'::regtype_a.identifier", []any{"integer", "integer", "integer"}},
			{"domain_user_literal", "'regtype_a.same'::regtype_a.identifier", []any{"same", "same", "same"}},
		}
		for _, tc := range cases {
			for _, target := range []string{"text", "varchar"} {
				for _, explicit := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						name := fmt.Sprintf("output_generated_%s_%s_%t_%t", tc.name, target, explicit, prepared)
						t.Run(name, func(t *testing.T) {
							expression := tc.expression
							if explicit {
								expression = "(" + expression + ")::" + target
							}
							query := "CREATE TABLE regtype_a." + name + "(id integer,r regtype,d regtype_a.identifier,g " + target + " GENERATED ALWAYS AS (" + expression + ") STORED)"
							stage := "execute"
							var result sql.Result
							var err error
							if prepared {
								var held *sql.Stmt
								held, err = conn.PrepareContext(p.Ctx, query)
								if err != nil {
									stage = "prepare"
								} else {
									defer held.Close()
									result, err = held.ExecContext(p.Ctx)
								}
							} else {
								result, err = conn.ExecContext(p.Ctx, query)
							}
							if stage != "execute" {
								t.Fatalf("generated declaration failed during %s, want execute: %v", stage, err)
							}
							if explicit {
								if result != nil {
									t.Fatal("failed generated declaration exposed result", result)
								}
								want := fixture.ControlsReferenceDiagnostic("42P17", "generation expression is not immutable")
								fixture.AssertDiagnostic(t, err, &want)
								missing := fixture.ErrorDiagnostic("SELECT * FROM regtype_a."+name, fixture.ControlsReferenceDiagnostic("42P01", `relation "regtype_a.`+name+`" does not exist`))
								missing.Diagnostic.Position = "15"
								fixture.RunStep(t, p.Ctx, conn, recorder, missing)
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO regtype_a."+name+"(id,r,d) VALUES(1,'integer','integer'),(2,'regtype_a.same','regtype_a.same'),(3,NULL,NULL)", 3))
							family := "TEXT"
							if target == "varchar" {
								family = "VARCHAR"
							}
							fixture.RegtypeBoth(t, p, conn, recorder, fixture.Select("SELECT id,g FROM regtype_a."+name+" ORDER BY id", []string{"id", "g"}, []string{"INT4", family}, [][]any{{int64(1), tc.values[0]}, {int64(2), tc.values[1]}, {int64(3), tc.values[2]}}))
							fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("DROP TABLE regtype_a."+name, 0))
						})
					}
				}
			}
		}
	})
}
