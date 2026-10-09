package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

type referenceCharacterNullability struct{ nullable, known bool }

func referenceCharacterMetadata(t *testing.T, p *referencePair, db *sql.DB, query string, prepared, native bool, names, types []string, lengths []int64, nullability []referenceCharacterNullability) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := db.PrepareContext(p.ctx, query)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.ctx)
	} else {
		rows, err = db.QueryContext(p.ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(types) {
		t.Fatal(columns, err)
	}
	if native && len(nullability) != len(columns) {
		t.Fatal("missing independent per-column nullability", query, nullability)
	}
	check := func() {
		for i, column := range columns {
			want := lengths[i]
			if want < 0 {
				want = math.MaxInt64
				if !native && types[i] != "TEXT" {
					want = -5
				}
			}
			length, known := column.Length()
			scan := reflect.TypeFor[string]()
			if !native && types[i] == "BPCHAR" {
				scan = reflect.TypeFor[any]()
			}
			precision, scale, decimal := column.DecimalSize()
			if column.Name() != names[i] || column.DatabaseTypeName() != types[i] || length != want || !known || column.ScanType() != scan || precision != 0 || scale != 0 || decimal {
				t.Fatalf("native=%t query=%q column=%d name=%s type=%s length=(%d,%t) scan=%v decimal=(%d,%d,%t)", native, query, i, column.Name(), column.DatabaseTypeName(), length, known, column.ScanType(), precision, scale, decimal)
			}
			value, known := column.Nullable()
			if native {
				if value != nullability[i].nullable || known != nullability[i].known {
					t.Fatal("native nullable contract", query, i, value, known, nullability[i])
				}
			} else if value || known {
				t.Fatal("pq nullable contract", value, known)
			}
		}
	}
	check()
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	check()
	rows.Close()
	check()
}
