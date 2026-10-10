package reference_test

import (
	"database/sql"
	"testing"
)

func datetimeExtractZones(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
	t.Helper()
	q := "SELECT EXTRACT(day FROM $1::timestamptz) AS day,EXTRACT(hour FROM $1::timestamptz) AS hour,date_part('minute',$1::timestamptz) AS minute,EXTRACT(timezone FROM $1::timestamptz) AS zone,date_part('epoch',$1::timestamptz) AS epoch"
	stmt, err := c.PrepareContext(p.ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	x := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
	for _, tc := range []struct {
		zone, stamp, day, hour, offset string
		minute, epoch                  float64
	}{
		{"UTC", "2020-01-01 00:00+00", "1", "0", "0", 0, 1577836800},
		{"America/New_York", "2020-01-01 00:00+00", "31", "19", "-18000", 0, 1577836800},
		{"America/New_York", "2020-07-01 00:00+00", "30", "20", "-14400", 0, 1593561600},
		{"Asia/Kathmandu", "2020-01-01 00:00+00", "1", "5", "20700", 45, 1577836800},
		{"UTC", "2020-01-01 00:00+00", "1", "0", "0", 0, 1577836800},
	} {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
		s := referenceSelect(q, []string{"day", "hour", "minute", "zone", "epoch"}, []string{"NUMERIC", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8"}, [][]any{{tc.day, tc.hour, tc.minute, tc.offset, tc.epoch}}, tc.stamp)
		referenceRunStep(t, p.ctx, x, b.recorder, s)
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		for _, fixed := range []struct{ clock, zone, hour, minute string }{{"00:00+05:30", "19800", "5", "30"}, {"00:00-05:30", "-19800", "-5", "-30"}, {"00:00+05:30:45", "19845", "5", "30"}} {
			for _, f := range []struct{ field, want string }{{"timezone", fixed.zone}, {"timezone_hour", fixed.hour}, {"timezone_minute", fixed.minute}} {
				datetimeExtractPair(t, p, b, c, f.field, "TIMETZ '"+fixed.clock+"'", f.want, datetimeExtractFloat(t, f.want))
			}
		}
	}
	// Different physical sessions cannot share a prepared extraction environment.
	peer, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	referenceRunStep(t, p.ctx, peer, b.recorder, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	datetimeExtractPair(t, p, b, peer, "hour", "TIMESTAMPTZ '2020-01-01 00:00+00'", "19", 19)
	datetimeExtractPair(t, p, b, c, "hour", "TIMESTAMPTZ '2020-01-01 00:00+00'", "0", 0)
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'Europe/Amsterdam'", 0))
	datetimeExtractPair(t, p, b, c, "timezone", "TIMESTAMPTZ '1800-01-01 00:00+00'", "1172", 1172)
	referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	datetimeExtractPair(t, p, b, c, "timezone", "TIMESTAMPTZ '2500-07-01 00:00+00'", "-14400", -14400)
}

func TestReferenceDatetimeExtractExecuteZones(t *testing.T) {
	datetimeExtractScenario(t, datetimeExtractZones)
}
