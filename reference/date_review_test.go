package reference_test

import (
	"testing"
)

// Independent expectations come from REL_18_0 parse_target.c
// transformAssignedExpr and parse_coerce.c's transformed literal location.
// In particular, DATE's format_type_be name has no time-zone suffix.
func TestReferenceDateTypedAssignmentDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE i(v INTEGER)", 0))
			run(referenceExec("INSERT INTO i VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				name := "direct"
				if prepared {
					name = "prepared"
				}
				t.Run(name, func(t *testing.T) {
					s := referenceImplicitError("INSERT INTO i VALUES(DATE '2000-01-01')", referenceDiagnostic{
						Code: "42804", Message: `column "v" is of type integer but expression is of type date`,
						Hint: "You will need to rewrite or cast the expression.", Position: "27",
					})
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, s)
				})
				// Also check after a diagnostic mismatch in the child subtest.
				run(referenceSelect("SELECT v FROM i ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

// REL_18_0 parse_type.c typenameTypeMod checks typmodin before converting
// raw expressions; DATE has no typmodin in pg_type.dat. Unary plus is an
// admitted expression even though it is not a simple modifier constant.
func TestReferenceDateModifierConversionPrecedence(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE d(v DATE)", 0))
			run(referenceExec("INSERT INTO d VALUES('infinity'),('2000-01-01')", 2))
			for _, typ := range []string{"date", `"date"`} {
				for _, modifier := range []string{"3.0", "2147483648", "999999999999999999999999999999", "+3", "+3.0", "+2147483648"} {
					for _, prepared := range []bool{false, true} {
						mode := "direct"
						if prepared {
							mode = "prepared"
						}
						t.Run(typ+"/"+modifier+"/"+mode, func(t *testing.T) {
							t.Run("create", func(t *testing.T) {
								s := referenceImplicitError("CREATE TABLE bad(v "+typ+"("+modifier+"))", referenceDiagnostic{
									Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "20",
								})
								s.prepared, s.errorStage = prepared, "execute"
								referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, s)
							})
							// CREATE Prepare is effect-free and succeeds; execution rejects
							// the modifier without installing even a partial relation.
							referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE TABLE bad(v DATE)", 0))
							referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("DROP TABLE bad", 0))
							t.Run("typed_literal", func(t *testing.T) {
								s := referenceImplicitError("SELECT "+typ+"("+modifier+") '1999-01-08' FROM d", referenceDiagnostic{
									Code: "42601", Message: `type modifier is not allowed for type "date"`, Position: "8",
								})
								s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
								if prepared {
									s.errorStage = "prepare"
								}
								referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, s)
							})
							referenceDateRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, [][]any{{referenceDateExpected(backend.pg, "2000-01-01")}, {"infinity"}}))
						})
					}
				}
			}
		})
	}
}
