package reference_test

import (
	"database/sql"
	"slices"
	"testing"
)

// Retained TIME cursors need the same raw/metadata observations as live steps.
func referenceTimeConsume(t *testing.T, rows *sql.Rows, pg bool, s referenceStep) [][]any {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !slices.Equal(columns, s.columns) {
		t.Fatal(columns, s.columns, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(s.types) {
		t.Fatal(meta, err)
	}
	for i, typ := range meta {
		if typ.DatabaseTypeName() != s.types[i] {
			t.Fatal(typ)
		}
	}
	referenceTemporalMetadata(t, pg, meta)
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
			referenceAssertRawValue(t, pg, s.types[i], value)
			row[i] = referenceNormalize(value)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	referenceTemporalMetadata(t, pg, meta)
	compared, matched := referenceCompareRows(values, s.rows, s.bag)
	if !matched {
		t.Fatalf("TIME rows %#v want %#v", values, s.rows)
	}
	return compared
}
