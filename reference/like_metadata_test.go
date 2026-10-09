package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func likeReferenceMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range metadata {
		if typ.DatabaseTypeName() == "BOOL" && typ.ScanType() != reflect.TypeFor[bool]() {
			t.Fatal("LIKE BOOL ScanType", typ.ScanType())
		}
	}
}
