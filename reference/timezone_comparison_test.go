package reference_test

import (
	"testing"
	"time"
)

func TestReferenceTimezoneGroupingExtremaJoinsAndCasts(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	p.step(t, referenceExec("CREATE TABLE tz_groups(id INTEGER,z TIMESTAMPTZ,q TIMETZ)", 0))
	p.step(t, referenceExec("INSERT INTO tz_groups VALUES(1,'2004-01-01 12:00+02','09:00+01'),(2,'2004-01-01 10:00+00','08:00+00'),(3,'2004-01-01 10:00+00','09:00+01')", 3))
	timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS z,count(*) AS n FROM tz_groups GROUP BY z", []string{"z", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2004-01-01 10:00:00+00", int64(3)}}))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS q,count(*) AS n FROM tz_groups GROUP BY q ORDER BY tz_groups.q", []string{"q", "n"}, []string{"TEXT", "INT8"}, [][]any{{"09:00:00+01", int64(2)}, {"08:00:00+00", int64(1)}}))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS q,z::TEXT AS z FROM tz_groups ORDER BY tz_groups.q", []string{"q", "z"}, []string{"TEXT", "TEXT"}, [][]any{{"09:00:00+01", "2004-01-01 10:00:00+00"}, {"09:00:00+01", "2004-01-01 10:00:00+00"}, {"08:00:00+00", "2004-01-01 10:00:00+00"}}))
	timezoneBoth(t, p, referenceSelect("SELECT a.id FROM tz_groups a JOIN tz_groups b(bid,bz,bq) ON a.q=b.bq WHERE b.bid=1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
	for _, backend := range p.backends() {
		var clock any = "09:00:00"
		if backend.pg {
			clock = time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)
		}
		s := referenceSelect("SELECT COALESCE(NULL::TIMESTAMPTZ,z)::TEXT AS z,NULLIF(q,TIMETZ '09:00+01') AS q,q::TIME AS c,DATE '2004-01-01'::TIMESTAMPTZ::TEXT AS d FROM tz_groups WHERE id=1", []string{"z", "q", "c", "d"}, []string{"TEXT", "TIMETZ", "TIME", "TEXT"}, [][]any{{"2004-01-01 10:00:00+00", nil, clock, "2004-01-01 00:00:00+00"}})
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
	}
}

func TestReferenceTimezoneColumnComparisonAndComposition(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE tz_comparison(z TIMESTAMPTZ,q TIMETZ)", 0))
	p.step(t, referenceExec("INSERT INTO tz_comparison VALUES('2004-10-19 04:00+00','09:00+01'),('2004-10-19 04:00+00','08:00+00'),(NULL,NULL)", 3))
	p.step(t, referenceExec("SET TIME ZONE 'America/New_York'", 0))
	timezoneBoth(t, p, referenceSelect("SELECT DATE '2004-10-19'=z AS a,z=TIMESTAMP '2004-10-19 00:00' AS b,DATE '2004-10-19' BETWEEN z AND z AS c,z IS DISTINCT FROM NULL AS d FROM tz_comparison WHERE z IS NOT NULL", []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}, {true, true, true, true}}))
	timezoneBoth(t, p, referenceSelect("SELECT z::TEXT AS z,count(*) AS n FROM tz_comparison WHERE z IS NOT NULL GROUP BY z", []string{"z", "n"}, []string{"TEXT", "INT8"}, [][]any{{"2004-10-19 00:00:00-04", int64(2)}}))
	timezoneBoth(t, p, referenceSelect("SELECT q::TEXT AS q FROM tz_comparison WHERE q IS NOT NULL ORDER BY tz_comparison.q", []string{"q"}, []string{"TEXT"}, [][]any{{"09:00:00+01"}, {"08:00:00+00"}}))
	for _, backend := range p.backends() {
		want := [][]any{{false, true, "10:34:17+00", "01:30:00+02"}}
		if backend.pg {
			want[0][2] = time.Date(0, 1, 1, 10, 34, 17, 0, time.UTC)
			// pq anchors 01:30+02 on year-zero January 1; UTC is the preceding day.
			want[0][3] = time.Date(-1, 12, 31, 23, 30, 0, 0, time.UTC)
		}
		s := referenceSelect("SELECT TIMETZ '09:00+01'=TIMETZ '08:00+00' AS a,TIMETZ '09:00+01'<TIMETZ '08:00+00' AS b,TIMETZ '05:34:17-05' AT TIME ZONE 'UTC' AS c,TIMETZ '23:30+00' AT TIME ZONE 'UTC-2' AS d FROM tz_comparison WHERE q=TIMETZ '09:00+01'", []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "TIMETZ", "TIMETZ"}, want)
		for _, prepared := range []bool{false, true} {
			step := s
			step.prepared = prepared
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
			if backend.pg {
				backend.recorder.mu.Lock()
				op := backend.recorder.operations[len(backend.recorder.operations)-1]
				backend.recorder.mu.Unlock()
				if op.SQL != s.query || len(op.Rows) != 1 || len(op.Rows[0]) != 4 {
					t.Fatal("pq TIMETZ conversion recording", op)
				}
				raw, ok := op.Rows[0][3].(time.Time)
				_, offset := raw.Zone()
				if !ok || raw.Year() != 0 || raw.Month() != time.January || raw.Day() != 1 || raw.Hour() != 1 || raw.Minute() != 30 || raw.Second() != 0 || raw.Nanosecond() != 0 || offset != 2*60*60 {
					t.Fatal("pq TIMETZ raw civil clock/offset", raw, offset)
				}
			}
		}
	}
	timezoneBoth(t, p, referenceSelect("SELECT (TIMESTAMP '2004-07-01 12:00' AT TIME ZONE 'America/New_York') AT TIME ZONE 'UTC' AS a,(TIMESTAMP '2004-07-01 12:00' AT LOCAL)::TEXT AS b,NULL::TIMESTAMPTZ AT LOCAL AS c,CASE WHEN TRUE THEN 'safe' ELSE (TIMESTAMP '2004-07-01' AT TIME ZONE 'No/Such_Zone')::TEXT END AS d FROM tz_comparison WHERE q=TIMETZ '09:00+01'", []string{"a", "b", "c", "d"}, []string{"TIMESTAMP", "TEXT", "TIMESTAMP", "TEXT"}, [][]any{{time.Date(2004, 7, 1, 16, 0, 0, 0, time.UTC), "2004-07-01 12:00:00-04", nil, "safe"}}))
	p.step(t, referenceExec("SET TIME ZONE 'UTC'", 0))
	timezoneBoth(t, p, referenceSelect("SELECT DATE '2004-10-19'=z AS a,z=TIMESTAMP '2004-10-19 00:00' AS b FROM tz_comparison WHERE q=TIMETZ '09:00+01'", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
}
