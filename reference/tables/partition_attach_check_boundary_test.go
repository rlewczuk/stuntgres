package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Attachment consumes existing CHECK facts; it does not add a prerequisite
// feature for general hierarchy CHECK alteration. Recursive unvalidated facts
// in Proof are created on ordinary standalone leaves before genuine ATTACH.
func partitionAttachCheckAlterBoundary(t *testing.T) {
	for _, target := range []string{"pa_boundary_parent", "pa_boundary_default"} {
		t.Run("inherited_check_alter_boundary/"+target, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE pa_boundary_parent(k INT) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_boundary_default PARTITION OF pa_boundary_parent DEFAULT", 0))
					q := "ALTER TABLE " + target + " ADD CONSTRAINT pa_boundary_check CHECK(k>=0) NOT VALID"
					s := fixture.Exec(q, 0)
					if !b.PG {
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "altering partition hierarchies is unsupported"})
					}
					run(s)
					run(fixture.Exec("INSERT INTO pa_boundary_parent VALUES(1)", 1))
					run(fixture.Select("SELECT k FROM pa_boundary_parent", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				})
			}
		})
	}
}
