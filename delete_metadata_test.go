package stuntgres_test

import (
	"context"
	"testing"
	"time"

	"stuntgres"
)

func TestDeleteCoreMetadataComment(t *testing.T) {
	for _, tc := range []struct{ name, comment string }{
		{"table", "COMMENT ON TABLE t IS 'metadata only'"},
		{"column", "COMMENT ON COLUMN t.id IS 'metadata only'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			for _, query := range []string{"CREATE TABLE t(id INT NOT NULL)", "INSERT INTO t VALUES(1),(2),(2)"} {
				if _, err := b.ExecContext(ctx, query); err != nil {
					t.Fatal(query, err)
				}
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, query := range []string{"INSERT INTO t VALUES(7)", "DELETE FROM t WHERE id=1"} {
				result, err := tx.ExecContext(ctx, query)
				if err != nil {
					t.Fatal(query, err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 1 {
					t.Fatalf("%s: count %d, %v; want 1", query, n, err)
				}
			}
			// COMMENT must finish while DELETE still holds RowExclusive: the
			// compatible metadata operation cannot be made to wait for commit.
			if _, err := b.ExecContext(ctx, tc.comment); err != nil {
				t.Fatal("compatible peer COMMENT", err)
			}
			rows, err := b.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}, false)
			rows, err = tx.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(7)}}, false)
			if err := tx.Commit(); err != nil {
				t.Fatal("metadata-only COMMENT invalidated staged DELETE and prior INSERT", err)
			}
			rows, err = b.QueryContext(ctx, "SELECT id FROM t ORDER BY id")
			queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {int64(7)}}, false)
		})
	}
}
