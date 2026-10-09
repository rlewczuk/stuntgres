package reference_test

import "testing"

func TestReferenceTimezoneReviewViewAt(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, s := range []referenceStep{
		referenceExec("SET TIME ZONE 'UTC'", 0),
		referenceExec("CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", 0),
		referencePrepared(referenceExec("CREATE VIEW tz_at AS SELECT id,value AT TIME ZONE zone AS explicit,value AT LOCAL AS local_value,TIMESTAMP(3) '2004-07-01 12:00:00.1234' AT TIME ZONE 'UTC' AS frozen FROM tz_at_source", 0)),
		referenceExec("CREATE VIEW tz_at_nested WITH(security_invoker=true) AS SELECT * FROM tz_at", 0),
		referenceExec("ALTER TABLE tz_at_source RENAME TO tz_at_pinned", 0),
		referenceExec("ALTER TABLE tz_at_pinned RENAME COLUMN value TO civil", 0),
		referenceExec("CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", 0),
		referenceExec("INSERT INTO tz_at_pinned VALUES(1,'2004-07-01 12:00:00.1234','America/New_York'),(2,NULL,'UTC'),(3,'2004-07-01 12:00',NULL)", 3),
		referenceExec("INSERT INTO tz_at_source VALUES(99,'2000-01-01','UTC')", 1),
	} {
		p.step(t, s)
	}
	p.step(t, referenceSelect("SELECT explicit,local_value FROM tz_at WHERE id=2", []string{"explicit", "local_value"}, []string{"TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{nil, nil}}))
	p.step(t, referencePrepared(referenceSelect("SELECT explicit,local_value FROM tz_at WHERE id=2", []string{"explicit", "local_value"}, []string{"TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{nil, nil}})))
	for _, backend := range p.backends() {
		query := timezoneReviewViewAtQuery
		held := timezoneHeld(t, p, backend.db, query)
		for _, tc := range []struct{ zone, local, whole string }{
			{"UTC", "2004-07-01 12:00:00.123", "2004-07-01 12:00:00"},
			{"America/New_York", "2004-07-01 16:00:00.123", "2004-07-01 16:00:00"},
			{"Asia/Tokyo", "2004-07-01 03:00:00.123", "2004-07-01 03:00:00"},
		} {
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := referenceSelect(query, []string{"id", "explicit", "local_value", "frozen"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00.123", tc.local, "2004-07-01 12:00:00.123"}, {int64(2), nil, nil, "2004-07-01 12:00:00.123"}, {int64(3), nil, tc.whole, "2004-07-01 12:00:00.123"}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			referenceRunStep(t, p.ctx, held, backend.recorder, s)
		}
	}
}
