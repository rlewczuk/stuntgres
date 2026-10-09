package stuntgres_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

// Shared public column contract for the remaining external consumer tests.
func characterMetadata(t *testing.T, db *sql.DB, q string, names []string, lengths []int64) {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != len(names) {
		t.Fatalf("metadata count %d want %d", len(types), len(names))
	}
	check := func() {
		for i, typ := range types {
			n, ok := typ.Length()
			_, _, decimal := typ.DecimalSize()
			wantLength := lengths[i]
			if wantLength < 0 {
				wantLength = math.MaxInt64
			}
			if typ.DatabaseTypeName() != names[i] || typ.ScanType() != reflect.TypeFor[string]() || decimal || !ok || n != wantLength {
				t.Fatalf("column %d type=%s scan=%v length=(%d,%t) decimal=%t want %s/%d", i, typ.DatabaseTypeName(), typ.ScanType(), n, ok, decimal, names[i], wantLength)
			}
		}
	}
	check()
	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	check()
}
