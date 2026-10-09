package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"testing"
)

// lexicalRows checks retained/unrecorded cursors before normalization. Ordinary
// streams still use referenceRunStep, including its owned middleware records.
func lexicalRows(t *testing.T, pg bool, rows *sql.Rows, s referenceStep, nullable []bool) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, s.columns) {
		t.Fatalf("columns %#v: %v, want %#v", columns, err, s.columns)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(s.types) {
		t.Fatalf("metadata width: %v", err)
	}
	for i, typ := range s.types {
		scan := reflect.TypeFor[int64]()
		switch typ {
		case "INT4":
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "TEXT":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "NUMERIC":
			scan = reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
		}
		if metadata[i].DatabaseTypeName() != typ || metadata[i].ScanType() != scan {
			t.Fatalf("column %d type/ScanType %s/%v, want %s/%v", i, metadata[i].DatabaseTypeName(), metadata[i].ScanType(), typ, scan)
		}
		if nullable != nil {
			value, known := metadata[i].Nullable()
			if pg {
				if value || known {
					t.Fatalf("pq Nullable %t/%t", value, known)
				}
			} else if !known || value != nullable[i] {
				t.Fatalf("native Nullable %t/%t, want %t/true", value, known, nullable[i])
			}
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(row))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			referenceAssertRawValue(t, pg, s.types[i], row[i])
			row[i] = referenceNormalize(row[i])
		}
		got = append(got, row)
	}
	if rows.Err() != nil || rows.Next() {
		t.Fatalf("cursor EOF: %v", rows.Err())
	}
	if _, ok := referenceCompareRows(got, s.rows, s.bag); !ok {
		t.Fatalf("rows %#v, want %#v", got, s.rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func lexicalOrdinary(t *testing.T, err, want error) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.Is(err, want) || errors.As(err, &state) {
		t.Fatalf("ordinary error %v, want %v without SQLSTATE", err, want)
	}
}

type lexicalFailIO struct{ err error }

func (f lexicalFailIO) Read([]byte) (int, error)  { return 0, f.err }
func (f lexicalFailIO) Write([]byte) (int, error) { return 0, f.err }

type lexicalCancelReader struct {
	cancel context.CancelFunc
	done   bool
}

func (r *lexicalCancelReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.cancel()
	return copy(p, "CREATE TABLE replacement(v INT);"), nil
}
