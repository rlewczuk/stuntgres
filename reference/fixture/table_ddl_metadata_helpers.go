package fixture

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TableDDLColumnTypes(t *testing.T, postgres bool, columns []*sql.ColumnType, names, types []string, nativeNullable []bool) {
	t.Helper()
	if len(columns) != len(names) || len(columns) != len(types) || len(columns) != len(nativeNullable) {
		t.Fatalf("column metadata widths: columns=%d names=%v types=%v nullable=%v", len(columns), names, types, nativeNullable)
	}
	for i, column := range columns {
		var scan reflect.Type
		switch types[i] {
		case "INT2", "INT4", "INT8":
			scan = reflect.TypeFor[int64]()
			if postgres && types[i] == "INT2" {
				scan = reflect.TypeFor[int16]()
			}
			if postgres && types[i] == "INT4" {
				scan = reflect.TypeFor[int32]()
			}
		case "OID":
			scan = reflect.TypeFor[int64]()
			if postgres {
				scan = reflect.TypeFor[any]()
			}
		case "PG_NODE_TREE":
			scan = reflect.TypeFor[string]()
			if postgres {
				scan = reflect.TypeFor[any]()
			}
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "BYTEA":
			scan = reflect.TypeFor[[]byte]()
		case "TIMESTAMP", "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		case "DATE", "TIME":
			scan = reflect.TypeFor[string]()
			if postgres {
				scan = reflect.TypeFor[time.Time]()
			}
		case "FLOAT4":
			scan = reflect.TypeFor[float64]()
			if postgres {
				scan = reflect.TypeFor[float32]()
			}
		case "FLOAT8":
			scan = reflect.TypeFor[float64]()
		case "BPCHAR":
			scan = reflect.TypeFor[string]()
			if postgres {
				scan = reflect.TypeFor[any]()
			}
		case "NUMERIC", "UUID":
			scan = reflect.TypeFor[string]()
			if postgres {
				scan = reflect.TypeFor[any]()
			}
		case "NAME":
			scan = reflect.TypeFor[string]()
			if postgres {
				scan = reflect.TypeFor[any]()
			}
		case "TEXT", "VARCHAR":
			scan = reflect.TypeFor[string]()
		default:
			t.Fatalf("missing independently specified ScanType for %q", types[i])
		}
		value, known := column.Nullable()
		wantValue, wantKnown := nativeNullable[i], true
		if postgres {
			wantValue, wantKnown = false, false
		}
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || column.ScanType() != scan || value != wantValue || known != wantKnown {
			t.Fatalf("postgres=%t column=%d name=%q type=%q scan=%v nullable=(%t,%t); want name=%q type=%q scan=%v nullable=(%t,%t)", postgres, i, column.Name(), column.DatabaseTypeName(), column.ScanType(), value, known, names[i], types[i], scan, wantValue, wantKnown)
		}
	}
}

func TableDDLMetadata(t *testing.T, ctx context.Context, db Executor, postgres, prepared bool, query string, names, types []string, nativeNullable []bool, count int) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := db.PrepareContext(ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := stmt.Close(); e != nil {
				t.Errorf("metadata statement close: %v", e)
			}
		}()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = db.QueryContext(ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := rows.Close(); e != nil {
			t.Errorf("metadata rows close: %v", e)
		}
	}()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatalf("metadata labels=%v err=%v want=%v", labels, err, names)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	TableDDLColumnTypes(t, postgres, columns, names, types, nativeNullable)
	actual := 0
	values, dest := make([]any, len(types)), make([]any, len(types))
	for i := range values {
		dest[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if !postgres && types[i] == "TIMESTAMP" && value != nil {
				stamp, ok := value.(time.Time)
				if !ok {
					t.Fatalf("raw native timestamp %T %v", value, value)
				}
				if _, offset := stamp.Zone(); offset != 0 {
					t.Fatalf("raw native timestamp offset=%d", offset)
				}
			}
		}
		actual++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if actual != count {
		t.Fatalf("metadata query %q count=%d want=%d", query, actual, count)
	}
}
