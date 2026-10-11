package fixture

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func DeparseReviewMetadata(t *testing.T, pg bool, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		var scan reflect.Type
		switch column.DatabaseTypeName() {
		case "INT2", "INT4", "INT8", "OID":
			scan = reflect.TypeFor[int64]()
		case "FLOAT4", "FLOAT8":
			scan = reflect.TypeFor[float64]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "BYTEA":
			scan = reflect.TypeFor[[]byte]()
		case "TIMESTAMP", "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		default:
			scan = reflect.TypeFor[string]()
		}
		if pg {
			switch column.DatabaseTypeName() {
			case "INT2":
				scan = reflect.TypeFor[int16]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			case "FLOAT4":
				scan = reflect.TypeFor[float32]()
			case "DATE", "TIME", "TIMETZ":
				scan = reflect.TypeFor[time.Time]()
			case "OID", "NAME", "BPCHAR", "CHAR", "NUMERIC", "UUID", "INTERVAL", "_NAME", "PG_NODE_TREE", "VOID":
				scan = reflect.TypeFor[any]()
			}
		}
		if column.ScanType() != scan {
			t.Fatal(column.Name(), column.DatabaseTypeName(), column.ScanType(), scan)
		}
	}
}
