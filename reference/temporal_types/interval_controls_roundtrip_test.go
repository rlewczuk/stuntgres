package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalControlRestoreAndRetainedStyle(t *testing.T) {
	p := fixture.NewScenario(t)
	ctx := p.Ctx
	drv := p.Native.Driver().(*stuntgres.Driver)
	db := p.Native
	destNative, dest, destRecord := fixture.IntervalReferenceDestination(t, p, t.Name()+"/restore")
	queryCtx := context.WithValue(ctx, fixture.RecorderKey{}, destRecord)
	p.Step(t, fixture.Exec("CREATE TABLE iv(id INTEGER,v INTERVAL,y INTERVAL YEAR,p INTERVAL SECOND(2) DEFAULT INTERVAL '1.235 seconds')", 0))
	p.Step(t, fixture.Exec("INSERT INTO iv VALUES(1,'-14 months 3 days -4:05:06','13 months','1.235 seconds'),(2,'24 hours',NULL,NULL),(3,'1 day',NULL,NULL),(4,'30 days',NULL,NULL),(5,'1 mon',NULL,NULL),(6,'infinity',NULL,NULL),(7,'-infinity',NULL,NULL),(8,NULL,NULL,NULL)", 8))
	p.Step(t, fixture.Exec("CREATE VIEW ivview AS SELECT INTERVAL '1.235 seconds' SECOND(2) AS v FROM iv WHERE id=1", 0))
	p.Step(t, fixture.Select("SELECT id,v,y,p FROM iv ORDER BY id", []string{"id", "v", "y", "p"}, []string{"INT4", "INTERVAL", "INTERVAL", "INTERVAL"}, [][]any{{int64(1), "-1 years -2 mons +3 days -04:05:06", "1 year", "00:00:01.24"}, {int64(2), "24:00:00", nil, nil}, {int64(3), "1 day", nil, nil}, {int64(4), "30 days", nil, nil}, {int64(5), "1 mon", nil, nil}, {int64(6), "infinity", nil, nil}, {int64(7), "-infinity", nil, nil}, {int64(8), nil, nil, nil}}))
	var canonical bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &canonical); err != nil {
		t.Fatal(err)
	}
	for _, style := range []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"} {
		fixture.IntervalNativeExec(t, db, "SET IntervalStyle="+style)
		var dump bytes.Buffer
		if err := stuntgres.Dump(ctx, db, &dump); err != nil || dump.String() != canonical.String() {
			t.Fatal(style, err, "style-dependent dump")
		}
	}
	if strings.Contains(canonical.String(), "SET IntervalStyle") || !strings.Contains(canonical.String(), "INTERVAL YEAR") || !strings.Contains(canonical.String(), "INTERVAL SECOND(2)") {
		t.Fatal(canonical.String())
	}
	fixture.IntervalNativeExec(t, dest, "SET IntervalStyle=sql_standard")
	if err := stuntgres.Restore(ctx, destNative, bytes.NewReader(canonical.Bytes())); err != nil {
		t.Fatal(err)
	}
	var redump bytes.Buffer
	if err := stuntgres.Dump(ctx, destNative, &redump); err != nil || redump.String() != canonical.String() {
		t.Fatal(err, redump.String(), canonical.String())
	}
	fixture.IntervalNativeExec(t, dest, "SET IntervalStyle=postgres")
	rows, err := dest.QueryContext(queryCtx, "SELECT id,v,y,p FROM iv ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	fixture.IntervalRawRows(t, rows, [][]any{{int64(1), "-1 years -2 mons +3 days -04:05:06", "1 year", "00:00:01.24"}, {int64(2), "24:00:00", nil, nil}, {int64(3), "1 day", nil, nil}, {int64(4), "30 days", nil, nil}, {int64(5), "1 mon", nil, nil}, {int64(6), "infinity", nil, nil}, {int64(7), "-infinity", nil, nil}, {int64(8), nil, nil, nil}}, destRecord)
	rows, err = dest.QueryContext(queryCtx, "SELECT v FROM ivview")
	if err != nil {
		t.Fatal(err)
	}
	fixture.IntervalRawRows(t, rows, [][]any{{"00:00:01.24"}}, destRecord)
	fixture.IntervalNativeExec(t, dest, "INSERT INTO iv(id) VALUES(9)")
	rows, err = dest.QueryContext(queryCtx, "SELECT p FROM iv WHERE id=9")
	if err != nil {
		t.Fatal(err)
	}
	fixture.IntervalRawRows(t, rows, [][]any{{"00:00:01.24"}}, destRecord)
	conn, err := drv.Open("reference/" + t.Name())
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
	var before bytes.Buffer
	if err = stuntgres.Dump(ctx, destNative, &before); err != nil {
		t.Fatal(err)
	}
	failed := "CREATE TABLE broken(v INTERVAL); INSERT INTO broken VALUES('1 day'),('invalid')"
	if err = stuntgres.Restore(ctx, destNative, strings.NewReader(failed)); err == nil {
		t.Fatal("failed restore succeeded")
	}
	fixture.IntervalDumpMatches(t, ctx, destNative, before.Bytes())
	var style string
	if err = dest.QueryRow("SHOW IntervalStyle").Scan(&style); err != nil || style != "postgres" {
		t.Fatal(style, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = stuntgres.Restore(canceled, destNative, bytes.NewReader(canonical.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fixture.IntervalDumpMatches(t, ctx, destNative, before.Bytes())
	if _, err = dest.ExecContext(canceled, "INSERT INTO iv VALUES(10,'1 day',NULL,NULL)"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fixture.IntervalDumpMatches(t, ctx, destNative, before.Bytes())
	rows, err = dest.QueryContext(queryCtx, "SELECT id FROM iv ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	fixture.IntervalRawRows(t, rows, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}, {int64(8)}, {int64(9)}}, destRecord)
}
