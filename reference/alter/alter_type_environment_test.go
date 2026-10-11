package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeEnvironmentAndStableDefault(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE utc_conversion(c TIMESTAMP)", 0),
		fixture.Exec("INSERT INTO utc_conversion VALUES(TIMESTAMP '2004-07-01 12:00')", 1),
		fixture.Exec("ALTER TABLE utc_conversion ALTER c TYPE TIMESTAMPTZ USING c::timestamptz", 0),
		fixture.Select("SELECT (c AT TIME ZONE 'UTC')::text AS utc FROM utc_conversion", []string{"utc"}, []string{"TEXT"}, [][]any{{"2004-07-01 12:00:00"}}),
		fixture.Exec("CREATE TABLE ny_conversion(c TIMESTAMP)", 0),
		fixture.Exec("INSERT INTO ny_conversion VALUES(TIMESTAMP '2004-07-01 12:00')", 1),
		fixture.Exec("CREATE TABLE t(c TEXT)", 0),
		fixture.Exec("INSERT INTO t VALUES('2004-07-01 12:00')", 1),
		fixture.Exec("SET TIME ZONE 'America/New_York'", 0),
		fixture.Exec("ALTER TABLE ny_conversion ALTER c TYPE TIMESTAMPTZ USING c::timestamptz", 0),
		fixture.Select("SELECT (c AT TIME ZONE 'UTC')::text AS utc FROM ny_conversion", []string{"utc"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}}),
		fixture.Exec("CREATE TABLE date_conversion(c TEXT)", 0),
		fixture.Exec("INSERT INTO date_conversion VALUES('19/10/2004')", 1),
		fixture.Exec("SET DateStyle='SQL,DMY'", 0),
		fixture.Exec("ALTER TABLE date_conversion ALTER c TYPE DATE USING c::date", 0),
		fixture.Select("SELECT c::text AS date FROM date_conversion", []string{"date"}, []string{"TEXT"}, [][]any{{"19/10/2004"}}),
		fixture.Exec("SET DateStyle='ISO,MDY'", 0),
		fixture.Select("SELECT c::text AS date FROM date_conversion", []string{"date"}, []string{"TEXT"}, [][]any{{"2004-10-19"}}),
		fixture.Exec("ALTER TABLE t ALTER c TYPE TIMESTAMPTZ USING c::timestamptz", 0),
		fixture.Select("SELECT (c AT TIME ZONE 'UTC')::text AS utc FROM t", []string{"utc"}, []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}}),
		fixture.Exec("SET TIME ZONE 'UTC'", 0),
		fixture.Exec("CREATE TABLE d(id INT,c TIMESTAMPTZ(6) DEFAULT (TIMESTAMP '2004-07-01 12:00' AT LOCAL))", 0),
		fixture.Exec("INSERT INTO d(id) VALUES(1)", 1),
		fixture.Exec("ALTER TABLE d ALTER c TYPE TIMESTAMPTZ(2)", 0),
		fixture.Exec("SET TIME ZONE 'America/New_York'", 0),
		fixture.Exec("INSERT INTO d(id) VALUES(2)", 1),
		fixture.Select("SELECT id,(c AT TIME ZONE 'UTC')::text AS utc FROM d ORDER BY id", []string{"id", "utc"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 16:00:00"}}),
	} {
		p.Step(t, s)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,(c AT TIME ZONE 'UTC')::text AS utc FROM d ORDER BY id", []string{"id", "utc"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 16:00:00"}}),
		fixture.Exec("SET TIME ZONE 'UTC'", 0),
		fixture.Exec("INSERT INTO d(id) VALUES(3)", 1),
		fixture.Select("SELECT id,(c AT TIME ZONE 'UTC')::text AS utc FROM d ORDER BY id", []string{"id", "utc"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "2004-07-01 12:00:00"}, {int64(2), "2004-07-01 16:00:00"}, {int64(3), "2004-07-01 12:00:00"}}),
	} {
		p.Step(t, s)
	}
}
