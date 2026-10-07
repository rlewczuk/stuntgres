package stuntgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestTimezoneBoundsInfinitiesAndProviderOffsets(t *testing.T) {
	ctx := context.Background()
	d := stuntgres.NewDriver()
	db := openLocal(t, d, t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ,q TIMETZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1,'-infinity','00:00+15:59:59'),(2,'infinity','24:00-15:59:59'),(3,'epoch',NULL)", 3)
	timezoneRows(t, c, "SELECT z::TEXT FROM tz_witness ORDER BY z", []string{"TEXT"}, [][]any{{"-infinity"}, {"1970-01-01 00:00:00+00"}, {"infinity"}})
	timezoneRows(t, c, "SELECT TIMESTAMPTZ '2004-01-01 24:00+00'::TEXT,TIMESTAMPTZ '2004-01-01 00:00+15:59:59'::TEXT FROM tz_witness WHERE id=1", []string{"TEXT", "TEXT"}, [][]any{{"2004-01-02 00:00:00+00", "2003-12-31 08:00:01+00"}})
	timezoneExec(t, c, "SET TIME ZONE 'UTC167:59:60'", 0)
	frozen, err := c.PrepareContext(ctx, "SELECT TIMETZ '12:00' FROM tz_witness WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer frozen.Close()
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(4,NULL,'12:00')", 1)
	timezoneExec(t, c, "SET TIME ZONE 'UTC'", 0)
	var q string
	if err = frozen.QueryRowContext(ctx).Scan(&q); err != nil || q != "12:00:00-168" {
		t.Fatal(q, err)
	}
	timezoneRows(t, c, "SELECT q,q AT TIME ZONE 'UTC' FROM tz_witness WHERE id=4", []string{"TIMETZ", "TIMETZ"}, [][]any{{"12:00:00-168", "12:00:00+00"}})
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(5,NULL,'2000-07-01 24:00 AAA-167:59:60BBB')", 1)
	timezoneRows(t, c, "SELECT q FROM tz_witness WHERE id=5", []string{"TIMETZ"}, [][]any{{"24:00:00+169"}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	dest := openLocal(t, d, t.Name()+"/restore")
	if err = stuntgres.Restore(ctx, dest, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	timezoneRows(t, dest, "SELECT q FROM tz_witness WHERE id=4", []string{"TIMETZ"}, [][]any{{"12:00:00-168"}})
	timezoneRows(t, dest, "SELECT q FROM tz_witness WHERE id=5", []string{"TIMETZ"}, [][]any{{"24:00:00+169"}})
	// Each infinity export owns its bytes, even though ScanType is time.Time.
	r, err := c.QueryContext(ctx, "SELECT z FROM tz_witness WHERE id=2")
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if !r.Next() {
		t.Fatal(r.Err())
	}
	if err = r.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	r.Close()
	raw.([]byte)[0] = 'X'
	timezoneRows(t, c, "SELECT z::TEXT FROM tz_witness WHERE id=2", []string{"TEXT"}, [][]any{{"infinity"}})
	for _, input := range []string{"2004-01-01 12:00+00:00:60", "2004-01-01 12:00-16"} {
		_, err = c.ExecContext(ctx, "INSERT INTO tz_witness(z) VALUES($1)", input)
		timeDiagnostic(t, err, stuntgres.Error{Code: "22009", Message: "time zone displacement out of range: \"" + input + "\""})
	}
	for _, sql := range []string{"INSERT INTO tz_witness(z) VALUES('2005-01-01+00'); INSERT INTO tz_witness(z) VALUES('2006-01-01+00')", "UPDATE tz_witness SET z='2005-01-01+00'; nonsense"} {
		if _, err = c.ExecContext(ctx, sql); err == nil {
			t.Fatal("prefix accepted", sql)
		}
	}
	timezoneRows(t, c, "SELECT z::TEXT FROM tz_witness WHERE id=3", []string{"TEXT"}, [][]any{{"1970-01-01 00:00:00+00"}})
	canceled, cancel := context.WithCancel(ctx)
	if err = stuntgres.Restore(canceled, dest, &cancelReader{cancel: cancel}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, dest, strings.NewReader(dump.String()+"CREATE TABLE bad(v TIMETZ); INSERT INTO bad VALUES('10:00 No/Such_Zone');")); err == nil {
		t.Fatal("unknown zone restore succeeded")
	}
	timezoneRows(t, dest, "SELECT q FROM tz_witness WHERE id=4", []string{"TIMETZ"}, [][]any{{"12:00:00-168"}})
}

func TestTimezoneGroupingExtremaJoinsAndCasts(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(id INTEGER,z TIMESTAMPTZ,q TIMETZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1,'2004-01-01 12:00+02','09:00+01'),(2,'2004-01-01 10:00+00','08:00+00'),(3,'2004-01-01 10:00+00','09:00+01')", 3)
	timezoneRows(t, c, "SELECT z::TEXT,count(*) FROM tz_witness GROUP BY z", []string{"TEXT", "INT8"}, [][]any{{"2004-01-01 10:00:00+00", int64(3)}})
	timezoneRows(t, c, "SELECT q::TEXT,count(*) FROM tz_witness GROUP BY q ORDER BY q", []string{"TEXT", "INT8"}, [][]any{{"09:00:00+01", int64(2)}, {"08:00:00+00", int64(1)}})
	// Equal q values also have identical projected z values, so duplicate ties
	// remain deterministic without requiring unsupported multiple sort keys.
	timezoneRows(t, c, "SELECT q::TEXT,z::TEXT FROM tz_witness ORDER BY q", []string{"TEXT", "TEXT"}, [][]any{{"09:00:00+01", "2004-01-01 10:00:00+00"}, {"09:00:00+01", "2004-01-01 10:00:00+00"}, {"08:00:00+00", "2004-01-01 10:00:00+00"}})
	timezoneRows(t, c, "SELECT a.id FROM tz_witness a JOIN tz_witness b(bid,bz,bq) ON a.q=b.bq WHERE b.bid=1 ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}})
	timezoneRows(t, c, "SELECT COALESCE(NULL::TIMESTAMPTZ,z)::TEXT,NULLIF(q,TIMETZ '09:00+01'),q::TIME,DATE '2004-01-01'::TIMESTAMPTZ::TEXT FROM tz_witness WHERE id=1", []string{"TEXT", "TIMETZ", "TIME", "TEXT"}, [][]any{{"2004-01-01 10:00:00+00", nil, "09:00:00", "2004-01-01 00:00:00+00"}})
}

func TestTimezoneFiniteEndpointsAndWideDateComparison(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v TIMESTAMPTZ)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES('4714-11-24 00:00+00 BC'),('294276-12-31 23:59:59.999999+00')", 2)
	timezoneRows(t, c, "SELECT v::TEXT FROM tz_witness ORDER BY v", []string{"TEXT"}, [][]any{{"4714-11-24 00:00:00+00 BC"}, {"294276-12-31 23:59:59.999999+00"}})
	timezoneRows(t, c, "SELECT DATE '5874897-12-31'>v,v<DATE '5874897-12-31' FROM tz_witness", []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}})
	for _, input := range []string{"4714-11-24 00:00+01 BC", "294276-12-31 23:59:59.999999-01"} {
		_, err := c.ExecContext(context.Background(), "INSERT INTO tz_witness VALUES($1)", input)
		timeDiagnostic(t, err, stuntgres.Error{Code: "22008", Message: "timestamp out of range: \"" + input + "\""})
	}
	timezoneRows(t, c, "SELECT count(*) FROM tz_witness", []string{"INT8"}, [][]any{{int64(2)}})
}
