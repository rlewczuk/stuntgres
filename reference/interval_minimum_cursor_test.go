package reference_test

import (
	"bytes"
	"database/sql/driver"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalMinimumTimeRetainedCursor(t *testing.T) {
	p := newReferenceScenario(t)
	ctx := p.ctx
	drv := p.native.Driver().(*stuntgres.Driver)
	db := p.native
	p.step(t, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES('-9223372036854775808 microseconds')", 1))
	raw, err := drv.Open("reference/" + t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=iso_8601", nil); err != nil {
		t.Fatal(err)
	}
	held, err := raw.(driver.QueryerContext).QueryContext(ctx, "SELECT v FROM iv", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Restore(ctx, db, bytes.NewBufferString("CREATE TABLE iv(v INTERVAL); INSERT INTO iv VALUES('9223372036854775807 microseconds')")); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.(driver.ExecerContext).ExecContext(ctx, "SET IntervalStyle=postgres", nil); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	values := make([]driver.Value, 1)
	if err := held.Next(values); err != nil || values[0] != "PT-2562047788H-54.775808S" {
		t.Fatal(values, err)
	}
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DELETE FROM iv", 1))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("INSERT INTO iv VALUES('9223372036854775807 microseconds')", 1))
	}
	p.step(t, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"2562047788:00:54.775807"}}))
	if name := held.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0); name != "INTERVAL" {
		t.Fatal(name)
	}
}
