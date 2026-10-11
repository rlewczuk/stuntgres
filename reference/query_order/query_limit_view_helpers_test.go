package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func queryLimitHeldBounds(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"id"}) {
		t.Fatal(names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "INT4" {
		t.Fatal(meta, err)
	}
	for _, want := range []int64{1, 2} {
		if !rows.Next() {
			t.Fatal("missing held bounds row", rows.Err())
		}
		var value any
		if err := rows.Scan(&value); err != nil || value != want {
			t.Fatal(value, want, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra held bounds row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
