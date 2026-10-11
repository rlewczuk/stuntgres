package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// pq exposes decoded typmod artifacts for unsized character/numeric results.
// Those adapter fields are asserted independently, not copied into native Usage.
func anyValueAssertMetadata(t *testing.T, pg bool, metadata []*sql.ColumnType) {
	t.Helper()
	for _, c := range metadata {
		name := c.DatabaseTypeName()
		scan := reflect.TypeFor[string]()
		switch name {
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "INT2", "INT4", "INT8", "OID":
			scan = reflect.TypeFor[int64]()
		case "FLOAT4", "FLOAT8":
			scan = reflect.TypeFor[float64]()
		case "BYTEA":
			scan = reflect.TypeFor[[]byte]()
		case "TIMESTAMP", "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		}
		if pg {
			switch name {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "FLOAT4":
				scan = reflect.TypeFor[float32]()
			case "DATE", "TIME", "TIMETZ":
				scan = reflect.TypeFor[time.Time]()
			case "CHAR", "BPCHAR", "NUMERIC", "UUID", "OID", "NAME", "INTERVAL", "REGCLASS", "VOID", "PG_NODE_TREE", "_NAME":
				scan = reflect.TypeFor[any]()
			}
		}
		if c.ScanType() != scan {
			t.Fatalf("%s %s ScanType %v, want %v", c.Name(), name, c.ScanType(), scan)
		}
		wantLength, wantLengthKnown := int64(0), false
		switch name {
		case "TEXT", "VARCHAR", "BPCHAR":
			wantLength, wantLengthKnown = math.MaxInt64, true
			if pg && name != "TEXT" {
				wantLength = -5
			}
		case "BYTEA":
			if pg {
				wantLength, wantLengthKnown = math.MaxInt64, true
			}
		}
		if n, k := c.Length(); n != wantLength || k != wantLengthKnown {
			t.Fatalf("%s %s Length %d/%v, want %d/%v", c.Name(), name, n, k, wantLength, wantLengthKnown)
		}
		wantPrecision, wantScale, wantDecimalKnown := int64(0), int64(0), false
		if pg && name == "NUMERIC" {
			wantPrecision, wantScale, wantDecimalKnown = 65535, 65531, true
		}
		if p, s, k := c.DecimalSize(); p != wantPrecision || s != wantScale || k != wantDecimalKnown {
			t.Fatalf("%s %s DecimalSize %d,%d/%v, want %d,%d/%v", c.Name(), name, p, s, k, wantPrecision, wantScale, wantDecimalKnown)
		}
		if n, k := c.Nullable(); n != !pg || k != !pg {
			t.Fatalf("%s %s Nullable %v/%v, want %v/%v", c.Name(), name, n, k, !pg, !pg)
		}
	}
}

func anyValueResult(t *testing.T, p *fixture.Pair, backend fixture.Backend, q string, prepared bool, columns, types []string, want [][]any, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
	var executor fixture.Executor = backend.DB
	if prepared {
		stmt, err := backend.DB.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor = fixture.HeldStatement{backend.DB, stmt, q}
	}
	rows, err := executor.QueryContext(ctx, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	anyValueAssertMetadata(t, backend.PG, metadata)
	got := fixture.AnyValueConsume(t, rows, backend.Recorder, q, columns, types)
	if !fixture.AnyValueRowsEqual(got, want) {
		t.Fatalf("%s rows %#v want %#v", q, got, want)
	}
}
