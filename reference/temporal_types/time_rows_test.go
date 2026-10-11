package reference_test

import (
	"database/sql"
	"slices"
	"stuntgres/reference/fixture"
	"testing"
)

// Retained TIME cursors need the same raw/metadata observations as live steps.
func referenceTimeConsume(t *testing.T, rows *sql.Rows, pg bool, s fixture.Step) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.Columns) {
		t.Fatal(columns, s.Columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(s.Types) {
		t.Fatal(meta, err)
	}
	for i, typ := range meta {
		if typ.DatabaseTypeName() != s.Types[i] {
			t.Fatal(typ)
		}
	}
	fixture.TemporalMetadata(t, pg, meta)
	values := make([][]any, 0)
	for rows.Next() {
		row, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			fixture.AssertRawValue(t, pg, s.Types[i], value)
			row[i] = fixture.Normalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.TemporalMetadata(t, pg, meta)
	compared, matched := fixture.CompareRows(values, s.Rows, s.Bag)
	if !matched {
		t.Fatalf("TIME rows %#v want %#v", values, s.Rows)
	}
	return compared
}
