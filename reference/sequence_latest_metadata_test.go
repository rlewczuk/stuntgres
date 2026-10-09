package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// Sequence compositions retain character typmods and adapter metadata even after Close.
func sequenceLatestMetadata(t *testing.T, p *referencePair, c *sql.Conn, pg bool) {
	t.Helper()
	rows, err := c.QueryContext(p.ctx, "SELECT c,v FROM copied")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 2 {
		t.Fatal(columns, err)
	}
	check := func() {
		for i, column := range columns {
			typ := []string{"BPCHAR", "VARCHAR"}[i]
			scan := reflect.TypeFor[string]()
			if pg && i == 0 {
				scan = reflect.TypeFor[any]()
			}
			length, known := column.Length()
			precision, scale, decimal := column.DecimalSize()
			if column.DatabaseTypeName() != typ || column.ScanType() != scan || !known || length != []int64{4, 3}[i] || decimal || precision != 0 || scale != 0 {
				t.Fatal(i, column, scan, length, known, precision, scale, decimal)
			}
			nullable, known := column.Nullable()
			if pg {
				if nullable || known {
					t.Fatal(nullable, known)
				}
			} else if !nullable || !known {
				t.Fatal(nullable, known)
			}
		}
	}
	check()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	check()
}
