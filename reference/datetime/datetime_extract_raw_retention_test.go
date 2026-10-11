package reference_test

import (
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Native-specific public driver control: PostgreSQL is not given an invented
// raw-cursor/session-discard equivalence. Zone/value constants are X6 goldens.
func TestReferenceDatetimeExtractNativeRawRetainedZoneAfterSessionClose(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE dx_retained(a timestamptz)", 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO dx_retained VALUES(TIMESTAMPTZ '2020-01-01 00:00+00')", 1))
	source, err := p.Native.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	peer, err := p.Memory.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
	query := "SELECT EXTRACT(hour FROM a) AS nh,date_part('hour',a) AS fh,EXTRACT(timezone FROM a) AS nz,date_part('timezone',a) AS fz,EXTRACT(epoch FROM a) AS ne,date_part('epoch',a) AS fe FROM dx_retained"
	var retained driver.Rows
	defer func() {
		if retained != nil {
			if err := retained.Close(); err != nil {
				t.Errorf("retained cleanup: %v", err)
			}
		}
	}()
	checkMetadata := func() {
		if !reflect.DeepEqual(retained.Columns(), []string{"nh", "fh", "nz", "fz", "ne", "fe"}) {
			t.Fatal("retained column names", retained.Columns())
		}
		for i := range 6 {
			family, scan := "NUMERIC", reflect.TypeFor[string]()
			if i%2 != 0 {
				family, scan = "FLOAT8", reflect.TypeFor[float64]()
			}
			if retained.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != family || retained.(driver.RowsColumnTypeScanType).ColumnTypeScanType(i) != scan {
				t.Fatal("retained descriptor", i)
			}
			if precision, scale, known := retained.(driver.RowsColumnTypePrecisionScale).ColumnTypePrecisionScale(i); precision != 0 || scale != 0 || known {
				t.Fatal("retained decimal metadata", i, precision, scale, known)
			}
			if length, known := retained.(driver.RowsColumnTypeLength).ColumnTypeLength(i); length != 0 || known {
				t.Fatal("retained length", i, length, known)
			}
			if nullable, known := retained.(driver.RowsColumnTypeNullable).ColumnTypeNullable(i); nullable || known {
				t.Fatal("retained nullability", i, nullable, known)
			}
		}
	}
	if err := source.Raw(func(raw any) error {
		executor, querier := raw.(driver.ExecerContext), raw.(driver.QueryerContext)
		if _, err := executor.ExecContext(p.Ctx, "SET TIME ZONE 'America/New_York'", nil); err != nil {
			return err
		}
		var err error
		retained, err = querier.QueryContext(p.Ctx, query, nil)
		if err != nil {
			return err
		}
		// Inspect without consuming: the entire cursor is retained across teardown.
		checkMetadata()
		_, err = executor.ExecContext(p.Ctx, "SET TIME ZONE 'UTC'", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	fresh := fixture.Select(query, []string{"nh", "fh", "nz", "fz", "ne", "fe"}, []string{"NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC", "FLOAT8"}, [][]any{{"0", float64(0), "0", float64(0), "1577836800.000000", float64(1577836800)}})
	fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, fresh)
	physicallyClosed := false
	err = source.Raw(func(raw any) error {
		if err := raw.(driver.Conn).Close(); err != nil {
			return err
		}
		physicallyClosed = true
		// Retire the database/sql handle rather than returning a closed physical
		// driver connection to its pool. Never reuse raw after this callback.
		return driver.ErrBadConn
	})
	if !physicallyClosed || !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("source physical discard", physicallyClosed, err)
	}
	checkMetadata()
	values := make([]driver.Value, 6)
	want := []driver.Value{"19", float64(19), "-18000", float64(-18000), "1577836800.000000", float64(1577836800)}
	if err := retained.Next(values); err != nil || !reflect.DeepEqual(values, want) {
		t.Fatalf("retained NY values %#v want %#v: %v", values, want, err)
	}
	if err := retained.Next(values); err != io.EOF {
		t.Fatal("retained EOF", err)
	}
	checkMetadata()
	if err := retained.Close(); err != nil {
		t.Fatal(err)
	}
	retained = nil
	// Fresh executions stay independent after the old cursor/source are gone.
	fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, fresh)
	fixture.RunStep(t, p.Ctx, peer, p.MemoryRecord, fixture.Prepared(fresh))
}
