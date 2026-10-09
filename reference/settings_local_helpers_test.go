package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func settingsLocalExec(t *testing.T, e referenceExecutor, q string, prepared bool, args ...any) {
	t.Helper()
	ctx := context.Background()
	var r sql.Result
	var err error
	if prepared {
		s, x := e.PrepareContext(ctx, q)
		if x != nil {
			t.Fatal(q, x)
		}
		defer s.Close()
		r, err = s.ExecContext(ctx, args...)
	} else {
		r, err = e.ExecContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	want := int64(0)
	if len(q) >= 6 && q[:6] == "SELECT" {
		want = 1
	}
	if err != nil || n != want {
		t.Fatal(q, n, err)
	}
}
func settingsLocalRead(t *testing.T, e referenceExecutor, q, want string, prepared bool, args ...any) {
	t.Helper()
	ctx := context.Background()
	var rows *sql.Rows
	var err error
	if prepared {
		s, x := e.PrepareContext(ctx, q)
		if x != nil {
			t.Fatal(q, x)
		}
		defer s.Close()
		rows, err = s.QueryContext(ctx, args...)
	} else {
		rows, err = e.QueryContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal(q, "no row", rows.Err())
	}
	var got string
	if err = rows.Scan(&got); err != nil || got != want {
		t.Fatalf("%s got %q want %q err %v", q, got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(q, rows.Err())
	}
}
func settingsLocalConn(t *testing.T) (*sql.Conn, *sql.Conn) {
	t.Helper()
	db := sessionPool(t, settingsTestDriver(t), t.Name())
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}
