package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneReviewSessionConditionalBehavior(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	const query = "SELECT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00') IS NULL AS n,CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS a,CASE TIMESTAMPTZ '2004-10-19 04:00+00' WHEN DATE '2004-10-19' THEN TRUE ELSE FALSE END AS b FROM tz_witness WHERE id=1"
	for _, backend := range p.Backends() {
		held := timezoneHeld(t, p, backend.DB, query)
		for _, tc := range []struct {
			zone string
			want bool
		}{{"UTC", false}, {"America/New_York", true}, {"UTC", false}} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := fixture.Select(query, []string{"n", "a", "b"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, tc.want}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		}
	}
}

func TestReferenceTimezoneReviewVolatileATBehavior(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("INSERT INTO tz_witness(id) VALUES(2),(3)", 2))
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			for _, suffix := range []string{"AT LOCAL", "AT TIME ZONE 'UTC'"} {
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE SEQUENCE tz_review_seq", 0))
				query := "SELECT ((CASE WHEN nextval('tz_review_seq')=2 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END) " + suffix + ")::TEXT AS v FROM tz_witness ORDER BY id"
				var x fixture.Executor = backend.DB
				if prepared {
					x = timezoneHeld(t, p, backend.DB, query)
				}
				for run := range 2 {
					want := [][]any{{"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}}
					if run == 0 {
						want[1][0] = "2004-10-19 00:00:00+00"
					}
					fixture.RunStep(t, p.Ctx, x, backend.Recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, want))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT currval('tz_review_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3 * (run + 1))}}))
				}
				if prepared {
					if err := x.(fixture.HeldStatement).Statement.Close(); err != nil {
						t.Fatal(err)
					}
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP SEQUENCE tz_review_seq", 0))
			}
		}
	}
}
