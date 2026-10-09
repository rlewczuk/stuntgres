package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
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

func anyValueResult(t *testing.T, p *referencePair, backend referenceBackend, q string, prepared bool, columns, types []string, want [][]any, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
	var executor referenceExecutor = backend.db
	if prepared {
		stmt, err := backend.db.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		executor = referenceHeldStatement{backend.db, stmt, q}
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
	anyValueAssertMetadata(t, backend.pg, metadata)
	got := anyValueConsume(t, rows, backend.recorder, q, columns, types)
	if !anyValueRowsEqual(got, want) {
		t.Fatalf("%s rows %#v want %#v", q, got, want)
	}
}

func anyValueRowsEqual(a, b [][]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i, row := range a {
		if len(row) != len(b[i]) {
			return false
		}
		for j, value := range row {
			if f, ok := value.(float64); ok {
				other, ok := b[i][j].(float64)
				if !ok || !(math.IsNaN(f) && math.IsNaN(other)) && math.Float64bits(f) != math.Float64bits(other) {
					return false
				}
			} else if !reflect.DeepEqual(value, b[i][j]) {
				return false
			}
		}
	}
	return true
}
