package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independent expectations come from REL_18_0 parse_target.c
// transformAssignedExpr and parse_coerce.c's transformed literal location.
// In particular, DATE's format_type_be name has no time-zone suffix.
func TestReferenceDateTypedAssignmentDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE i(v INTEGER)", 0))
			run(fixture.Exec("INSERT INTO i VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					s := fixture.ImplicitError("INSERT INTO i VALUES(DATE '2000-01-01')", fixture.Diagnostic{
						Code: "42804", Message: `column "v" is of type integer but expression is of type date`,
						Hint: "You will need to rewrite or cast the expression.", Position: "27",
					})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				})
				// Also check after a diagnostic mismatch in the child subtest.
				run(fixture.Select("SELECT v FROM i ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

// REL_18_0 parse_type.c typenameTypeMod checks typmodin before converting
// raw expressions; DATE has no typmodin in pg_type.dat. Unary plus is an
// admitted expression even though it is not a simple modifier constant.
func TestReferenceDateModifierConversionPrecedence(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE)", 0))
			run(fixture.Exec("INSERT INTO d VALUES('infinity'),('2000-01-01')", 2))
			for _, typ := range []string{"date", `"date"`} {
				for _, modifier := range []string{"3.0", "2147483648", "999999999999999999999999999999", "+3", "+3.0", "+2147483648"} {
					for _, prepared := range []bool{false, true} {
						mode := "direct"
						if prepared {
							mode = "prepared"
						}
						t.Run(typ+"/"+modifier+"/"+mode, func(t *testing.T) {
							t.Run("create", func(t *testing.T) {
								s := fixture.ImplicitError("CREATE TABLE bad(v "+typ+"("+modifier+"))", fixture.Diagnostic{
									Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20",
								})
								s.Prepared, s.ErrorStage = prepared, "execute"
								fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							})
							// CREATE Prepare is effect-free and succeeds; execution rejects
							// the modifier without installing even a partial relation.
							fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bad(v DATE)", 0))
							fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE bad", 0))
							t.Run("typed_literal", func(t *testing.T) {
								s := fixture.ImplicitError("SELECT "+typ+"("+modifier+") '1999-01-08' FROM d", fixture.Diagnostic{
									Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8",
								})
								s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
								if prepared {
									s.ErrorStage = "prepare"
								}
								fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
							})
							fixture.DateRunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, [][]any{{fixture.DateExpected(backend.PG, "2000-01-01")}, {"infinity"}}))
						})
					}
				}
			}
		})
	}
}
