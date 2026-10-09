package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

type timezoneQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func timezoneRows(t *testing.T, c timezoneQueryer, q string, types []string, want [][]any, args ...any) {
	t.Helper()
	r, err := c.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer r.Close()
	meta, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != len(types) {
		t.Fatalf("%s metadata %v", q, meta)
	}
	for i, typ := range types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatalf("%s type[%d]=%s want %s", q, i, meta[i].DatabaseTypeName(), typ)
		}
	}
	got := make([][]any, 0)
	for r.Next() {
		row := make([]any, len(types))
		dst := make([]any, len(types))
		for i := range row {
			dst[i] = &row[i]
		}
		if err = r.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = r.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q, " ORDER BY ") {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %#v want %#v", q, got, want)
		}
		return
	}
	// Match unordered results as bags, including every duplicate occurrence.
	if len(got) != len(want) {
		t.Fatalf("%s cardinality %d want %d: %#v", q, len(got), len(want), got)
	}
	used := make([]bool, len(want))
	for _, row := range got {
		found := false
		for i, expected := range want {
			if !used[i] && reflect.DeepEqual(row, expected) {
				used[i] = true
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s bag occurrence %#v: %#v want %#v", q, row, got, want)
		}
	}
}
func timezoneExec(t *testing.T, c *sql.Conn, q string, n int64, args ...any) {
	t.Helper()
	r, err := c.ExecContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	got, err := r.RowsAffected()
	if err != nil || got != n {
		t.Fatalf("%s count %d %v want %d", q, got, err, n)
	}
}
