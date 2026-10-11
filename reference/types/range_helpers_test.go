package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func rangeLiteral(input string) string {
	return "'" + strings.ReplaceAll(input, "'", "''") + "'"
}

// Check both the public any-scan and middleware-owned stream before normalization.
func rangeSelect(backend fixture.Backend, query string, columns, types []string, values [][]any, nullable []bool) fixture.Step {
	s := fixture.Select(query, columns, types, values)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		if types[column] != "INT4RANGE" && types[column] != "INT8RANGE" && types[column] != "NUMRANGE" {
			return false
		}
		if value != nil {
			want := reflect.TypeFor[string]()
			if backend.PG {
				want = reflect.TypeFor[[]byte]()
			}
			if reflect.TypeOf(value) != want {
				t.Fatalf("range column %d row %d raw %T, want %v", column, row, value, want)
			}
			if bytes, ok := value.([]byte); ok && bytes == nil {
				t.Fatal("typed nil bytes are not SQL NULL")
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		metadata, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, typ := range metadata {
			if types[i] != "INT4RANGE" && types[i] != "INT8RANGE" && types[i] != "NUMRANGE" {
				continue
			}
			want := reflect.TypeFor[string]()
			if backend.PG {
				want = reflect.TypeFor[any]()
			}
			if typ.ScanType() != want {
				t.Fatalf("%s ScanType %v, want %v", types[i], typ.ScanType(), want)
			}
			if length, known := typ.Length(); length != 0 || known {
				t.Fatalf("%s length %d/%v", types[i], length, known)
			}
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("%s precision/scale %d/%d/%v", types[i], precision, scale, known)
			}
			n, known := typ.Nullable()
			if backend.PG {
				if n || known {
					t.Fatalf("pq nullable %v/%v", n, known)
				}
			} else if !known || n != nullable[i] {
				t.Fatalf("native nullable %v/%v, want %v/true", n, known, nullable[i])
			}
		}
	}
	return s
}

func rangeConsumeHeld(t *testing.T, rows *sql.Rows, check fixture.Step) {
	t.Helper()
	defer rows.Close()
	check.CheckMetadata(t, rows)
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, typ := range types {
		if typ.Name() != check.Columns[i] || typ.DatabaseTypeName() != check.Types[i] {
			t.Fatal("held column changed", i, typ)
		}
	}
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(check.Columns))
		dst := make([]any, len(row))
		for i := range row {
			dst[i] = &row[i]
		}
		if err := rows.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, value := range row {
			if !check.CheckRaw(t, i, len(got), value) {
				fixture.AssertRawValue(t, false, check.Types[i], value)
			}
			row[i] = fixture.Normalize(value)
		}
		got = append(got, row)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, check.Rows) {
		t.Fatalf("held rows=%#v want=%#v err=%v", got, check.Rows, rows.Err())
	}
}
