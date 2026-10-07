package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestIntervalNativeRoundTripAndRetainedStyle(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	db := openLocal(t, drv, t.Name())
	dest := openLocal(t, drv, t.Name()+"/restore")
	execSQL(t, db, "CREATE TABLE iv(id INTEGER,v INTERVAL,y INTERVAL YEAR,p INTERVAL SECOND(2) DEFAULT INTERVAL '1.235 seconds')")
	execSQL(t, db, "INSERT INTO iv VALUES(1,'-14 months 3 days -4:05:06','13 months','1.235 seconds'),(2,'24 hours',NULL,NULL),(3,'1 day',NULL,NULL),(4,'30 days',NULL,NULL),(5,'1 mon',NULL,NULL),(6,'infinity',NULL,NULL),(7,'-infinity',NULL,NULL),(8,NULL,NULL,NULL)")
	execSQL(t, db, "CREATE VIEW ivview AS SELECT INTERVAL '1.235 seconds' SECOND(2) AS v FROM iv WHERE id=1")
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		execSQL(t, db, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
	}
	if strings.Contains(canonical.String(), "SET IntervalStyle") || !strings.Contains(canonical.String(), "INTERVAL YEAR") || !strings.Contains(canonical.String(), "INTERVAL SECOND(2)") {
		t.Fatal(canonical.String())
	}
	execSQL(t, dest, "SET IntervalStyle=sql_standard")
	if err := stuntgres.Restore(ctx, dest, bytes.NewReader(canonical.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, dest, &redump); err != nil || redump.String() != canonical.String() {
		t.Fatal(err, redump.String(), canonical.String())
	}
	execSQL(t, dest, "SET IntervalStyle=postgres")
	rows, err := dest.Query("SELECT id,v,y,p FROM iv ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1), "-1 years -2 mons +3 days -04:05:06", "1 year", "00:00:01.24"}, {int64(2), "24:00:00", nil, nil}, {int64(3), "1 day", nil, nil}, {int64(4), "30 days", nil, nil}, {int64(5), "1 mon", nil, nil}, {int64(6), "infinity", nil, nil}, {int64(7), "-infinity", nil, nil}, {int64(8), nil, nil, nil}})
	rows, err = dest.Query("SELECT v FROM ivview")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{"00:00:01.24"}})
	execSQL(t, dest, "INSERT INTO iv(id) VALUES(9)")
	rows, err = dest.Query("SELECT p FROM iv WHERE id=9")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{"00:00:01.24"}})
	conn, err := drv.Open(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=iso_8601", nil); err != nil {
		t.Fatal(err)
	}
	held, err := conn.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM iv WHERE id=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if _, err = conn.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=postgres", nil); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err = held.Next(values); err != nil || values[0] != "P-1Y-2M3DT-4H-5M-6S" {
		t.Fatal(values, err)
	}
	if held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INTERVAL" || held.(driver.RowsColumnTypeScanType).ColumnTypeScanType(0) != reflect.TypeFor[string]() {
		t.Fatal("retained interval metadata")
	}
	failed := "CREATE TABLE broken(v INTERVAL); INSERT INTO broken VALUES('1 day'),('invalid')"
	if err = stuntgres.Restore(ctx, dest, strings.NewReader(failed)); err == nil {
		t.Fatal("failed restore succeeded")
	}
	var style string
	if err = dest.QueryRow("SHOW IntervalStyle").Scan(&style); err != nil || style != "postgres" {
		t.Fatal(style, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, dest, bytes.NewReader(canonical.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err = dest.ExecContext(canceled, "INSERT INTO iv VALUES(10,'1 day',NULL,NULL)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	rows, err = dest.Query("SELECT id FROM iv ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}, {int64(8)}, {int64(9)}})
}

func TestIntervalDiagnosticsAndAtomicity(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE iv(v INTERVAL)")
	execSQL(t, db, "INSERT INTO iv VALUES('1 day')")
	for _, tc := range []struct{ input, code, primary string }{{"invalid", "22007", "invalid input syntax for type interval"}, {"1 second 2 seconds", "22007", "invalid input syntax for type interval"}, {"5.5 seconds 3 milliseconds", "22007", "invalid input syntax for type interval"}, {"2147483648 days", "22015", "interval field value out of range"}, {"2147483647 years", "22008", "interval out of range"}} {
		message := tc.primary
		if tc.code != "22008" {
			message += ": \"" + tc.input + "\""
		}
		_, err := db.Exec("INSERT INTO iv VALUES($1),($2)", "1 mon", tc.input)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: message})
		rows, err := db.Query("SELECT v FROM iv")
		if err != nil {
			t.Fatal(err)
		}
		intervalRows(t, rows, [][]any{{"1 day"}})
	}
	for _, prepared := range []bool{false, true} {
		q := "INSERT INTO iv VALUES('1 mon'),('invalid')"
		var err error
		if prepared {
			_, err = db.Prepare(q)
		} else {
			_, err = db.Exec(q)
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type interval: \"invalid\"", Position: "33"})
		rows, e := db.Query("SELECT v FROM iv")
		if e != nil {
			t.Fatal(e)
		}
		intervalRows(t, rows, [][]any{{"1 day"}})
	}
	for _, q := range []string{"SELECT INTERVAL 'invalid' FROM iv", "SELECT /*é*/ INTERVAL 'invalid' FROM iv"} {
		_, err := db.Prepare(q)
		position := "17"
		if strings.Contains(q, "é") {
			position = "23"
		}
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22007", Message: "invalid input syntax for type interval: \"invalid\"", Position: position})
	}
	_, err := db.Exec("SET IntervalStyle=invalid")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid value for parameter \"IntervalStyle\": \"invalid\"", Hint: "Available values: postgres, postgres_verbose, sql_standard, iso_8601."})
	execSQL(t, db, "CREATE TABLE nn(v INTERVAL NOT NULL)")
	_, err = db.Exec("INSERT INTO nn VALUES('1 day'),(NULL)")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: "null value in column \"v\" of relation \"nn\" violates not-null constraint", Detail: "Failing row contains (null).", Schema: "public", Table: "nn", Column: "v"})
	rows, err := db.Query("SELECT v FROM nn")
	if err != nil {
		t.Fatal(err)
	}
	intervalRows(t, rows, nil)
}

func TestIntervalISOHexadecimalNativeRoundTrip(t *testing.T) {
	ctx := context.Background()
	drv := stuntgres.NewDriver()
	source := openLocal(t, drv, t.Name())
	execSQL(t, source, "CREATE TABLE iv(id INTEGER,v INTERVAL DEFAULT INTERVAL 'PT0x.8p0S')")
	execSQL(t, source, "INSERT INTO iv VALUES(1,'PT0x1.8S'),(2,'P1DT-0x1.8p1S')")
	execSQL(t, source, "INSERT INTO iv(id) VALUES(3)")
	execSQL(t, source, "CREATE VIEW ivview AS SELECT INTERVAL 'PT0X1.8P+1S' AS v FROM iv WHERE id=1")
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, source, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		execSQL(t, source, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, source, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent hexadecimal-origin dump")
		}
		dest := openLocal(t, drv, t.Name()+"/"+style)
		execSQL(t, dest, "SET IntervalStyle="+style)
		if err := stuntgres.Restore(ctx, dest, bytes.NewReader(canonical.Bytes())); err != nil {
			t.Fatal(style, err)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(ctx, dest, &redump); err != nil || redump.String() != canonical.String() {
			t.Fatal(style, err, "noncanonical hexadecimal-origin roundtrip")
		}
		execSQL(t, dest, "SET IntervalStyle=postgres")
		rows, err := dest.Query("SELECT id,v FROM iv ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		intervalRows(t, rows, [][]any{{int64(1), "00:00:01.5"}, {int64(2), "1 day -00:00:03"}, {int64(3), "00:00:00.5"}})
		rows, err = dest.Query("SELECT v FROM ivview")
		if err != nil {
			t.Fatal(err)
		}
		intervalRows(t, rows, [][]any{{"00:00:03"}})
		execSQL(t, dest, "INSERT INTO iv(id) VALUES(4)")
		rows, err = dest.Query("SELECT v FROM iv WHERE id=4")
		if err != nil {
			t.Fatal(err)
		}
		intervalRows(t, rows, [][]any{{"00:00:00.5"}})
	}
}
