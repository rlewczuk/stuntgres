package reference_test

import (
	"database/sql"
	"testing"
)

// Preserve the legacy smoke's nullable base-column descriptors on the actual
// source cursor; pq deliberately reports nullability as unknown.
func deparseReviewNullableColumns(t *testing.T, pg bool, rows *sql.Rows, names ...string) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		for _, name := range names {
			if column.Name() != name {
				continue
			}
			nullable, known := column.Nullable()
			if pg {
				if known {
					t.Fatal(name, nullable, known)
				}
			} else if !known || !nullable {
				t.Fatal(name, nullable, known)
			}
		}
	}
}
