package fixture

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"testing"
)

// LexicalRows checks retained/unrecorded cursors before normalization. Ordinary
// streams still use RunStep, including its owned middleware records.
func LexicalRows(t *testing.T, pg bool, rows *sql.Rows, s Step, nullable []bool) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, s.Columns) {
		t.Fatalf("columns %#v: %v, want %#v", columns, err, s.Columns)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(s.Types) {
		t.Fatalf("metadata width: %v", err)
	}
	for i, typ := range s.Types {
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
			AssertRawValue(t, pg, s.Types[i], row[i])
			row[i] = Normalize(row[i])
		}
		got = append(got, row)
	}
	if rows.Err() != nil || rows.Next() {
		t.Fatalf("cursor EOF: %v", rows.Err())
	}
	if _, ok := CompareRows(got, s.Rows, s.Bag); !ok {
		t.Fatalf("rows %#v, want %#v", got, s.Rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func LexicalOrdinary(t *testing.T, err, want error) {
	t.Helper()
	var state interface{ SQLState() string }
	if !errors.Is(err, want) || errors.As(err, &state) {
		t.Fatalf("ordinary error %v, want %v without SQLSTATE", err, want)
	}
}

type LexicalFailIO struct{ Err error }

func (f LexicalFailIO) Read([]byte) (int, error)  { return 0, f.Err }
func (f LexicalFailIO) Write([]byte) (int, error) { return 0, f.Err }

type LexicalCancelReader struct {
	Cancel context.CancelFunc
	done   bool
}

func (r *LexicalCancelReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	r.Cancel()
	return copy(p, "CREATE TABLE replacement(v INT);"), nil
}
