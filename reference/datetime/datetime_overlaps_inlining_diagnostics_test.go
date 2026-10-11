package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsInliningDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ov_inline(id integer,s timestamp,e timestamp)", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ov_inline VALUES(1,TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')", 1))
			q := "SELECT (TIMESTAMP '294276-12-31',INTERVAL '1 day') OVERLAPS (s,e) FROM ov_inline"
			d := fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range")
			d.Context = "SQL function \"overlaps\" during inlining"
			datetimeOverlapsFailure(t, p, b, c, q, d)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM ov_inline ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
			datetimeOverlapsValue(t, p, b, c, "(TIMESTAMP '2000-01-01',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')", true)
		})
	}
}
