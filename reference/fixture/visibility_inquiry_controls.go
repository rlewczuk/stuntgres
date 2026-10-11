package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func VisibilityBoolMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() == "BOOL" && typ.ScanType() != reflect.TypeFor[bool]() {
			t.Fatal(typ.Name(), typ.ScanType())
		}
	}
}
