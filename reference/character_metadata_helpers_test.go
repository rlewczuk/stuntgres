package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"testing"
)

type referenceCharacterNullability struct{ nullable, known bool }

func referenceCharacterMetadata(t *testing.T, p *referencePair, db *sql.DB, query string, prepared, native bool, names, types []string, lengths []int64, nullability []referenceCharacterNullability) {
	t.Helper()
	var rows *sql.Rows
	var err error
	record := p.postgresRecord
	if native {
		record = p.memoryRecord
	}
	ctx := context.WithValue(p.ctx, referenceRecorderKey{}, record)
	if prepared {
		stmt, e := db.PrepareContext(ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = db.QueryContext(ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(types) {
		t.Fatal(columns, err)
	}
	if native && len(nullability) != len(columns) {
		t.Fatal("missing independent per-column nullability", query, nullability)
	}
	check := func() {
		for i, column := range columns {
			want := lengths[i]
			if want < 0 {
				want = math.MaxInt64
				if !native && types[i] != "TEXT" {
					want = -5
				}
			}
			length, known := column.Length()
			scan := reflect.TypeFor[string]()
			if !native && types[i] == "BPCHAR" {
				scan = reflect.TypeFor[any]()
			}
			precision, scale, decimal := column.DecimalSize()
			if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || length != want || !known || column.ScanType() != scan || precision != 0 || scale != 0 || decimal {
				t.Fatalf("native=%t query=%q column=%d name=%s type=%s length=(%d,%t) scan=%v decimal=(%d,%d,%t)", native, query, i, column.Name(), column.DatabaseTypeName(), length, known, column.ScanType(), precision, scale, decimal)
			}
			value, known := column.Nullable()
			if native {
				if value != nullability[i].nullable || known != nullability[i].known {
					t.Fatal("native nullable contract", query, i, value, known, nullability[i])
				}
			} else if value || known {
				t.Fatal("pq nullable contract", value, known)
			}
		}
	}
	check()
	values := make([]any, len(types))
	destinations := make([]any, len(types))
	for i := range values {
		destinations[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			referenceAssertRawValue(t, !native, types[i], value)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	check()
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check()
	record.mu.Lock()
	var captured *referenceOperation
	for _, operation := range record.operations {
		if operation.SQL == query && operation.Columns != nil {
			captured = operation
		}
	}
	record.mu.Unlock()
	if captured == nil || !reflect.DeepEqual(captured.Columns, names) || !captured.Closed || captured.CloseErr != nil || captured.Err != nil {
		t.Fatal("missing consumed character metadata capture")
	}
	for _, row := range captured.Rows {
		for i, value := range row {
			referenceAssertRawValue(t, !native, types[i], value)
		}
	}
}
