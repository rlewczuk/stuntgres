package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTableViewsFailedIndexHistory(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, concurrent := range []bool{false, true} {
				t.Run(map[bool]string{false: "Ordinary", true: "Concurrent"}[concurrent], func(t *testing.T) {
					name := "pgtv_failed_plain"
					prefix := "CREATE INDEX "
					if concurrent {
						name = "pgtv_failed_concurrent"
						prefix = "CREATE INDEX CONCURRENTLY "
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE "+name+"(n INT)", 0))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO "+name+" VALUES(0)", 1))
					s := fixture.ErrorDiagnostic(prefix+name+"_index ON "+name+"((10/n))", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					// Pinned18.0 ordinary failed expression builds leave false; concurrent
					// invalid descriptors have already been published and retain true.
					pgTableCheck(t, p, b, b.DB, name, fixture.PGTableRow("public", name, fixture.PGTableViewsOwner(b), concurrent))
					if concurrent {
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP INDEX "+name+"_index", 0))
						pgTableCheck(t, p, b, b.DB, name, fixture.PGTableRow("public", name, fixture.PGTableViewsOwner(b), true))
					}
				})
			}
		})
	}
}
