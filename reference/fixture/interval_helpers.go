package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

// Destinations share their native and recorded connector, not the legacy global driver.
func IntervalReferenceDestination(t *testing.T, p *Pair, suffix string) (*sql.DB, *sql.DB, *Recorder) {
	t.Helper()
	connector, err := p.Native.Driver().(*stuntgres.Driver).OpenConnector("reference/" + t.Name() + "/" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	native := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := native.Close(); err != nil {
			t.Error(err)
		}
	})
	recorded, recorder := InsertSelectRecordedPool(t, connector)
	return native, recorded, recorder
}

// Raw native control observations deliberately precede fixture normalization.
func IntervalRawRows(t *testing.T, rows *sql.Rows, want [][]any, records ...*Recorder) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(names) {
		t.Fatal(meta, names, err)
	}
	for _, m := range meta {
		if m.DatabaseTypeName() == "INTERVAL" {
			if m.ScanType() != reflect.TypeFor[string]() {
				t.Fatal(m.ScanType())
			}
			if _, _, ok := m.DecimalSize(); ok {
				t.Fatal("interval numeric metadata")
			}
			if _, ok := m.Length(); ok {
				t.Fatal("interval length metadata")
			}
		}
	}
	var got [][]any
	for rows.Next() {
		row, dest := make([]any, len(names)), make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %#v; want %#v", got, want)
	}
	for _, recorder := range records {
		recorder.Mu.Lock()
		var op *Operation
		for _, candidate := range recorder.Operations {
			if candidate.Columns != nil && candidate.Err == nil {
				op = candidate
			}
		}
		recorder.Mu.Unlock()
		if op == nil || !op.Closed || op.CloseErr != nil {
			t.Fatal("unrecorded or unclosed native control observation", op)
		}
		captured := make([][]any, len(op.Rows))
		for i, row := range op.Rows {
			captured[i] = make([]any, len(row))
			for j, value := range row {
				captured[i][j] = value
			}
		}
		if !reflect.DeepEqual(op.Columns, names) || !reflect.DeepEqual(captured, want) {
			t.Fatal("recorded raw control output", op.Columns, captured, want)
		}
	}
}

func IntervalNativeExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(query, err)
	}
}

func IntervalReferenceMetadata(t *testing.T, p *Pair, backend Backend, query string, column int) {
	t.Helper()
	rows, err := backend.DB.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(names) {
		t.Fatal(meta, names, err)
	}
	m := meta[column]
	want := reflect.TypeFor[string]()
	if backend.PG {
		want = reflect.TypeFor[any]()
	}
	if m.DatabaseTypeName() != "INTERVAL" || m.ScanType() != want {
		t.Fatal(m, want)
	}
	if _, _, ok := m.DecimalSize(); ok {
		t.Fatal("interval numeric metadata")
	}
	if _, ok := m.Length(); ok {
		t.Fatal("interval length metadata")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func IntervalDumpMatches(t *testing.T, ctx context.Context, db *sql.DB, want []byte) {
	t.Helper()
	var got bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &got); err != nil || !bytes.Equal(got.Bytes(), want) {
		t.Fatal(err, "failed control changed catalog")
	}
}
