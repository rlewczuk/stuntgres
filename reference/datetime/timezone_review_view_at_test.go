package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimezoneReviewViewAt(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, s := range []fixture.Step{
		fixture.Exec("SET TIME ZONE 'UTC'", 0),
		fixture.Exec("CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", 0),
		fixture.Prepared(fixture.Exec("CREATE VIEW tz_at AS SELECT id,value AT TIME ZONE zone AS explicit,value AT LOCAL AS local_value,TIMESTAMP(3) '2004-07-01 12:00:00.1234' AT TIME ZONE 'UTC' AS frozen FROM tz_at_source", 0)),
		fixture.Exec("CREATE VIEW tz_at_nested WITH(security_invoker=true) AS SELECT * FROM tz_at", 0),
		fixture.Exec("ALTER TABLE tz_at_source RENAME TO tz_at_pinned", 0),
		fixture.Exec("ALTER TABLE tz_at_pinned RENAME COLUMN value TO civil", 0),
		fixture.Exec("CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", 0),
		fixture.Exec("INSERT INTO tz_at_pinned VALUES(1,'2004-07-01 12:00:00.1234','America/New_York'),(2,NULL,'UTC'),(3,'2004-07-01 12:00',NULL)", 3),
		fixture.Exec("INSERT INTO tz_at_source VALUES(99,'2000-01-01','UTC')", 1),
	} {
		p.Step(t, s)
	}
	p.Step(t, fixture.Select("SELECT explicit,local_value FROM tz_at WHERE id=2", []string{"explicit", "local_value"}, []string{"TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{nil, nil}}))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT explicit,local_value FROM tz_at WHERE id=2", []string{"explicit", "local_value"}, []string{"TIMESTAMPTZ", "TIMESTAMPTZ"}, [][]any{{nil, nil}})))
	for _, backend := range p.Backends() {
		query := timezoneReviewViewAtQuery
		held := timezoneHeld(t, p, backend.DB, query)
		for _, tc := range []struct{ zone, local, whole string }{
			{"UTC", "2004-07-01 12:00:00.123", "2004-07-01 12:00:00"},
			{"America/New_York", "2004-07-01 16:00:00.123", "2004-07-01 16:00:00"},
			{"Asia/Tokyo", "2004-07-01 03:00:00.123", "2004-07-01 03:00:00"},
		} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			s := fixture.Select(query, []string{"id", "explicit", "local_value", "frozen"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00.123", tc.local, "2004-07-01 12:00:00.123"}, {int64(2), nil, nil, "2004-07-01 12:00:00.123"}, {int64(3), nil, tc.whole, "2004-07-01 12:00:00.123"}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		}
	}
}
