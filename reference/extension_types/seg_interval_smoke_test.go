package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSegIntervalFeatureSmoke(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, step := range segIntervalConstructionChecks("public") {
			segIntervalBoth(t, p, b, c, step)
		}
		segIntervalStoredSetup(t, p, b, c)
		segIntervalBoth(t, p, b, c, segIntervalTableCheck(b))
		q := "SELECT $1::seg && $2::seg AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			a, b any
			want any
		}{{"0 .. 1", "1 .. 2", true}, {"0 .. 1", "2 .. 3", false}, {nil, "1", nil}, {"1", "1", true}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, segIntervalSelect(b, q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, false, tc.a, tc.b))
		}
		prefix := "INSERT INTO seg_interval_data VALUES (6,'1','1'),(7,"
		want := fixture.ControlsReferenceDiagnostic("42601", "bad seg representation")
		want.Detail = `syntax error at or near "A"`
		want.Position = segIntervalPosition(prefix)
		queryIntError(t, p, b, c, prefix+"'ABC'::seg,'1')", false, false, true, want)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, segIntervalTableCheck(b))
		prefix = "INSERT INTO seg_interval_defaults(id,overlap) VALUES (6,'1'::seg && '1'::seg),(7,"
		want.Position = segIntervalPosition(prefix)
		queryIntError(t, p, b, c, prefix+"'ABC'::seg && '1'::seg)", false, false, true, want)
		segIntervalStoredChecks(t, p, b, c, false)
		if !b.PG {
			segIntervalNativeControls(t, p, b, c)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP EXTENSION seg CASCADE", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE seg_interval_data,seg_interval_defaults,seg_interval_checked", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION seg", 0))
		for _, step := range segIntervalConstructionChecks("public") {
			segIntervalBoth(t, p, b, c, step)
		}
		segIntervalStoredSetup(t, p, b, c)
		segIntervalStoredChecks(t, p, b, c, false)
	})
}
