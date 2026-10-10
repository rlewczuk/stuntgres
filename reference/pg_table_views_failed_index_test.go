package reference_test

import "testing"

func TestReferencePGTableViewsFailedIndexHistory(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, concurrent := range []bool{false, true} {
				t.Run(map[bool]string{false: "Ordinary", true: "Concurrent"}[concurrent], func(t *testing.T) {
					name := "pgtv_failed_plain"
					prefix := "CREATE INDEX "
					if concurrent {
						name = "pgtv_failed_concurrent"
						prefix = "CREATE INDEX CONCURRENTLY "
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE "+name+"(n INT)", 0))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO "+name+" VALUES(0)", 1))
					s := referenceErrorDiagnostic(prefix+name+"_index ON "+name+"((10/n))", controlsReferenceDiagnostic("22012", "division by zero"))
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					// Pinned18.0 ordinary failed expression builds leave false; concurrent
					// invalid descriptors have already been published and retain true.
					pgTableCheck(t, p, b, b.db, name, pgTableRow("public", name, pgTableViewsOwner(b), concurrent))
					if concurrent {
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DROP INDEX "+name+"_index", 0))
						pgTableCheck(t, p, b, b.db, name, pgTableRow("public", name, pgTableViewsOwner(b), true))
					}
				})
			}
		})
	}
}
