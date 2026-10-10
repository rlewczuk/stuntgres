package reference_test

import "testing"

func TestReferenceDatetimeOverlapsInliningDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE ov_inline(id integer,s timestamp,e timestamp)", 0))
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO ov_inline VALUES(1,TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')", 1))
			q := "SELECT (TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (s,e) FROM ov_inline"
			d := controlsReferenceDiagnostic("22008", "timestamp out of range")
			d.Context = "SQL function \"overlaps\" during inlining"
			datetimeOverlapsFailure(t, p, b, c, q, d)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id FROM ov_inline ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			datetimeOverlapsValue(t, p, b, c, "(TIMESTAMP '2000-01-01',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')", true)
		})
	}
}
