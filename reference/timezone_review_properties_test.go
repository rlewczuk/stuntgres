package reference_test

import "testing"

func TestReferenceTimezoneReviewSessionConditionalBehavior(t *testing.T) {
	p := timezoneReferencePair(t)
	const query = "SELECT NULLIF(DATE '2004-10-19',TIMESTAMPTZ '2004-10-19 04:00+00') IS NULL AS n,CASE DATE '2004-10-19' WHEN TIMESTAMPTZ '2004-10-19 04:00+00' THEN TRUE ELSE FALSE END AS a,CASE TIMESTAMPTZ '2004-10-19 04:00+00' WHEN DATE '2004-10-19' THEN TRUE ELSE FALSE END AS b FROM tz_witness WHERE id=1"
	for _, backend := range p.backends() {
		held := timezoneHeld(t, p, backend.db, query)
		for _, tc := range []struct {
			zone string
			want bool
		}{{"UTC", false}, {"America/New_York", true}, {"UTC", false}} {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := referenceSelect(query, []string{"n", "a", "b"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, tc.want}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			referenceRunStep(t, p.ctx, held, backend.recorder, s)
		}
	}
}

func TestReferenceTimezoneReviewVolatileATBehavior(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("INSERT INTO tz_witness(id) VALUES(2),(3)", 2))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			for _, suffix := range []string{"AT LOCAL", "AT TIME ZONE 'UTC'"} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("CREATE SEQUENCE tz_review_seq", 0))
				query := "SELECT ((CASE WHEN nextval('tz_review_seq')=2 THEN TIMESTAMP '2004-10-19 00:00' ELSE TIMESTAMP 'epoch' END) " + suffix + ")::TEXT AS v FROM tz_witness ORDER BY id"
				var x referenceExecutor = backend.db
				if prepared {
					x = timezoneHeld(t, p, backend.db, query)
				}
				for run := range 2 {
					want := [][]any{{"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}, {"1970-01-01 00:00:00+00"}}
					if run == 0 {
						want[1][0] = "2004-10-19 00:00:00+00"
					}
					referenceRunStep(t, p.ctx, x, backend.recorder, referenceSelect(query, []string{"v"}, []string{"TEXT"}, want))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT currval('tz_review_seq') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3 * (run + 1))}}))
				}
				if prepared {
					if err := x.(referenceHeldStatement).statement.Close(); err != nil {
						t.Fatal(err)
					}
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("DROP SEQUENCE tz_review_seq", 0))
			}
		}
	}
}
