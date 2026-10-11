package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"
)

// UPDATE's transaction/count witnesses additionally require unsupported identities.
func updateCoreAffected(t *testing.T, result sql.Result, count int64) {
	t.Helper()
	LockAffected(t, result, count)
	id, err := result.LastInsertId()
	if id != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatal("LastInsertId", id, err)
	}
}

func UpdateCoreExec(t *testing.T, ctx context.Context, x Executor, q string, prepared bool, count int64, args ...any) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		stmt, e := x.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := stmt.Close(); e != nil {
				t.Error(e)
			}
		}()
		result, err = stmt.ExecContext(ctx, args...)
	} else {
		result, err = x.ExecContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	updateCoreAffected(t, result, count)
}

// Retained UPDATE snapshots cannot be queried again after replacement. Check the
// original public cursor, including its unchanged descriptor and exported data.
func UpdateCoreRetained(t *testing.T, pg bool, rows *sql.Rows, columns, types []string, want [][]any, bag bool) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	gotTypes := make([]string, len(metadata))
	for i, c := range metadata {
		gotTypes[i] = c.DatabaseTypeName()
	}
	if !reflect.DeepEqual(names, columns) || !reflect.DeepEqual(gotTypes, types) {
		t.Fatal(names, gotTypes)
	}
	got := [][]any{}
	for rows.Next() {
		row := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range row {
			AssertRawValue(t, pg, types[i], v)
			row[i] = Normalize(v)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := CompareRows(got, want, bag); !ok {
		t.Fatalf("retained rows got %#v want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

// Composition readbacks retain the public BYTEA value contract before the
// ordinary fixture compares complete normalized rows and owned recordings.
func UpdateCoreByteRows(t *testing.T, ctx context.Context, x Executor, query string, want [][][]byte) {
	t.Helper()
	rows, err := x.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(want[0]) {
		t.Fatal(metadata, err)
	}
	for _, column := range metadata {
		if column.DatabaseTypeName() != "BYTEA" || column.ScanType() != reflect.TypeFor[[]byte]() {
			t.Fatal(column.DatabaseTypeName(), column.ScanType())
		}
	}
	for row, expected := range want {
		if !rows.Next() {
			t.Fatal("missing raw BYTEA row", row, rows.Err())
		}
		values := make([]any, len(expected))
		dest := make([]any, len(expected))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for column, bytesWanted := range expected {
			if bytesWanted == nil {
				if values[column] != nil {
					t.Fatalf("raw BYTEA row%d column%d got %T %#v want SQL NULL", row, column, values[column], values[column])
				}
				continue
			}
			got, ok := values[column].([]byte)
			if !ok || got == nil || !bytes.Equal(got, bytesWanted) {
				t.Fatalf("raw BYTEA row%d column%d got %T %#v want nonnil %#v", row, column, values[column], values[column], bytesWanted)
			}
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("raw BYTEA completion", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
