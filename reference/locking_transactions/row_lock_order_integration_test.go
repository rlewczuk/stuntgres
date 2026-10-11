package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockOrderingIntegrationFailingFirst(t *testing.T) {
	p := referenceViewFixture(t)
	p.Step(t, fixture.Exec("CREATE VIEW ordered_stored AS SELECT id,v FROM t WHERE id<3 ORDER BY v+1 DESC,id ASC FOR UPDATE", 0))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, clause := range fixture.RowClauses {
				for _, prepared := range []bool{false, true} {
					for _, test := range []struct {
						query, column string
						values        [][]any
					}{
						{"SELECT v+1 AS val FROM t WHERE id<3 ORDER BY val DESC NULLS FIRST,id ASC " + clause, "val", [][]any{{int64(21)}, {int64(11)}}},
						{"SELECT val+1 AS shifted FROM w ORDER BY shifted DESC NULLS LAST,id ASC " + clause, "shifted", [][]any{{int64(22)}, {int64(12)}}},
						{"SELECT v+1 AS val FROM ordered_stored ORDER BY val DESC NULLS FIRST,id ASC", "val", [][]any{{int64(21)}, {int64(11)}}},
					} {
						step := fixture.Select(test.query, []string{test.column}, []string{"INT4"}, test.values)
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					}
				}
			}
		})
	}
	if *fixture.Mode {
		referenceRowOrderingWaits(t)
	}
}
