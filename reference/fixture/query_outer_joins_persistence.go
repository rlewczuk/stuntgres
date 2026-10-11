package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

// Held eager results retain their schema and owned values across native catalog
// replacement. PostgreSQL exercises the SQL/view portion of the same scenario.
func OuterJoinsHeldRows(t *testing.T, rows *sql.Rows, want Step) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, want.Columns) {
		t.Fatalf("held columns %v: %v; want %v", names, err, want.Columns)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(columns))
	for i, c := range columns {
		types[i] = c.DatabaseTypeName()
	}
	if !reflect.DeepEqual(types, want.Types) {
		t.Fatalf("held types %v want %v", types, want.Types)
	}
	var values [][]any
	for rows.Next() {
		row := make([]any, len(columns))
		destinations := make([]any, len(row))
		for i := range row {
			destinations[i] = &row[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = Normalize(row[i])
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CompareRows(values, want.Rows, true); !ok {
		t.Fatalf("held rows %#v want %#v", values, want.Rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
