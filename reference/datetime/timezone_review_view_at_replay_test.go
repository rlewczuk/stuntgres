package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

const timezoneReviewViewAtDDL = "CREATE VIEW tz_at AS SELECT id,value AT TIME ZONE zone AS explicit,value AT LOCAL AS local_value,TIMESTAMP(3) '2004-07-01 12:00:00.1234' AT TIME ZONE 'UTC' AS frozen FROM tz_at_source"
const timezoneReviewViewAtQuery = "SELECT id,(explicit AT TIME ZONE 'UTC')::TEXT AS explicit,(local_value AT TIME ZONE 'UTC')::TEXT AS local_value,(frozen AT TIME ZONE 'UTC')::TEXT AS frozen FROM tz_at_nested ORDER BY id"

func TestReferenceTimezoneReviewViewAtReplay(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, q := range []string{"SET TIME ZONE 'America/New_York'", "CREATE TABLE tz_at_source(id INT,value TIMESTAMP(3),zone TEXT)", timezoneReviewViewAtDDL, "CREATE VIEW tz_at_nested AS SELECT * FROM tz_at", "ALTER TABLE tz_at_source RENAME TO tz_at_pinned", "ALTER TABLE tz_at_pinned RENAME COLUMN value TO civil"} {
		p.Step(t, fixture.Exec(q, 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO tz_at_pinned VALUES(1,'2004-07-01 12:00:00.1234','America/New_York'),(2,NULL,'UTC')", 2))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dump.String(), "AT LOCAL") || !strings.Contains(dump.String(), "AT TIME ZONE") {
		t.Fatal("lost timezone definition", dump.String())
	}
	native, target, record := timezoneDestination(t)
	fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
	if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SHOW TimeZone", []string{"TimeZone"}, []string{"TEXT"}, [][]any{{"Asia/Tokyo"}}))
	s := fixture.Select(timezoneReviewViewAtQuery, []string{"id", "explicit", "local_value", "frozen"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "2004-07-01 16:00:00.123", "2004-07-01 03:00:00.123", "2004-07-01 12:00:00.123"}, {int64(2), nil, nil, "2004-07-01 12:00:00.123"}})
	fixture.RunStep(t, p.Ctx, target, record, s)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatal("canonical view replay", dump.String(), redump.String())
	}
	p.Step(t, fixture.Exec("SET TIME ZONE 'Asia/Tokyo'", 0))
	timezoneBoth(t, p, s)
}
