package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbElementsType(f string) string {
	if strings.HasSuffix(f, "_text") {
		return "TEXT"
	}
	return "JSONB"
}

func jsonbElementsSelect(f, query string, columns []string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(query, columns, []string{jsonbElementsType(f)}, rows, args...)
}

func jsonbElementsMetadata(s fixture.Step, pg bool) fixture.Step {
	return jsonbElementsColumnMetadata(s, pg, false)
}

func jsonbElementsViewMetadata(s fixture.Step, pg bool) fixture.Step {
	return jsonbElementsColumnMetadata(s, pg, true)
}

func jsonbElementsColumnMetadata(s fixture.Step, pg, stored bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		meta, err := r.ColumnTypes()
		if err != nil || len(meta) != len(s.Types) {
			t.Fatalf("metadata=%v error=%v", meta, err)
		}
		for i, ct := range meta {
			scan := reflect.TypeFor[string]()
			if s.Types[i] == "INT8" {
				scan = reflect.TypeFor[int64]()
			}
			if pg && s.Types[i] == "JSONB" {
				scan = reflect.TypeFor[any]()
			}
			if ct.Name() != s.Columns[i] || ct.DatabaseTypeName() != s.Types[i] || ct.ScanType() != scan {
				t.Errorf("column %d=%s/%s/%v want %s/%s/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), s.Columns[i], s.Types[i], scan)
			}
			length, known := int64(0), false
			if s.Types[i] == "TEXT" {
				length, known = 9223372036854775807, true
			}
			if n, k := ct.Length(); n != length || k != known {
				t.Errorf("Length=%d/%t want %d/%t", n, k, length, known)
			}
			// Stored view columns retain native known-nullable relation
			// metadata; computed expressions and pq report unknown nullability.
			nullable := stored && !pg
			if n, k := ct.Nullable(); n != nullable || k != nullable {
				t.Errorf("Nullable=%t/%t want=%t/%t", n, k, nullable, nullable)
			}
			if p, d, k := ct.DecimalSize(); p != 0 || d != 0 || k {
				t.Errorf("DecimalSize=%d/%d/%t", p, d, k)
			}
		}
	}
	return s
}

const jsonbElementsMixed = `[1,1.00,1.230e-5,true,false,"a\nb","",null,[2,{"z":1,"a":2}],{"zz":0,"a":1,"a":2},"null"]`

func jsonbElementsMixedRows(f string) [][]any {
	values := []any{"1", "1.00", "0.00001230", "true", "false", `"a\nb"`, `""`, "null", `[2, {"a": 2, "z": 1}]`, `{"a": 2, "zz": 0}`, `"null"`}
	if jsonbElementsType(f) == "TEXT" {
		values[5], values[6], values[7], values[10] = "a\nb", "", nil, "null"
	}
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v, int64(i + 1)}
	}
	return rows
}

func jsonbElementsOrdered(f string, input any, rows [][]any) fixture.Step {
	return fixture.Select("SELECT * FROM "+f+"($1) WITH ORDINALITY AS e(v,n) ORDER BY n", []string{"v", "n"}, []string{jsonbElementsType(f), "INT8"}, rows, input)
}
