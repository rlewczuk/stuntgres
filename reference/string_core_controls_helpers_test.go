package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// Retained cursors cannot be reissued through the step runner: these checks
// consume the actual captured cursor, including its raw cells before normalization.
func stringCoreRetainedRows(t *testing.T, rows *sql.Rows, err error, names, types []string, want [][]any, bag bool) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || names != nil && !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(types) {
		t.Fatal(meta, err)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal(i, meta[i], typ)
		}
		if typ == "BOOL" && meta[i].ScanType() != reflect.TypeFor[bool]() {
			t.Fatal(meta[i])
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		values, dest := make([]any, len(types)), make([]any, len(types))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			switch types[i] {
			case "BOOL":
				if v != nil {
					if _, ok := v.(bool); !ok {
						t.Fatalf("raw BOOL %T", v)
					}
				}
			case "INT4", "INT8":
				if v != nil {
					if _, ok := v.(int64); !ok {
						t.Fatalf("raw integer %T", v)
					}
				}
			case "TEXT", "BPCHAR", "VARCHAR", "NAME":
				if v != nil {
					if _, ok := v.(string); !ok {
						t.Fatalf("raw text/name %T", v)
					}
				}
			}
			values[i] = referenceNormalize(v)
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_, matched := referenceCompareRows(got, want, bag)
	if !matched {
		t.Fatal(got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("closed retained cursor", rows.Err())
	}
}

func stringCoreControlDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, err, &want)
}

func stringCoreControlExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	if result == nil {
		t.Fatal("nil successful result")
	}
	if _, err := result.RowsAffected(); err != nil {
		t.Fatal(err)
	}
	if _, err := result.LastInsertId(); err == nil {
		t.Fatal("LastInsertId supported")
	}
}

func stringCoreControlText(t *testing.T, db *sql.DB, prepared bool, query string, want any, args ...any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := db.Prepare(query)
		if e != nil {
			t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.Query(args...)
	} else {
		rows, err = db.Query(query, args...)
	}
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "TEXT" || meta[0].ScanType() != reflect.TypeFor[string]() {
		rows.Close()
		t.Fatal(meta, err)
	}
	stringCoreRetainedRows(t, rows, nil, []string{"d"}, []string{"TEXT"}, [][]any{{want}}, false)
}

// Native-control sentinels retain the original scanned-tuple contract. Logical
// descriptors for the shared SQL counterpart are independently supplied to steps.
func stringCoreControlTuples(t *testing.T, db *sql.DB, query string, want [][]any) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		t.Fatal(err)
	}
	types := make([]string, len(metadata))
	for i, typ := range metadata {
		types[i] = typ.DatabaseTypeName()
	}
	stringCoreRetainedRows(t, rows, nil, nil, types, want, false)
}
