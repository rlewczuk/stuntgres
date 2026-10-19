package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

type fdwWriteContextKey struct{}

func fdwWritePreparedContext(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader) {
	t.Helper()
	beforeTransactions, beforeBegins := r.writer.transactions, r.writer.modifies
	q := "INSERT INTO fdw_data VALUES($1,$2,true)"
	stmt, err := c.PrepareContext(context.WithValue(p.Ctx, fdwWriteContextKey{}, "prepare"), q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if r.writer.transactions != beforeTransactions || r.writer.modifies != beforeBegins {
		t.Fatal("Prepare entered foreign execution")
	}
	for i := range 2 {
		r.writer.writeHook = func(ctx context.Context, name string) error {
			if ctx.Value(fdwWriteContextKey{}) != i {
				return fmt.Errorf("%s received stale operation context: %v", name, ctx.Value(fdwWriteContextKey{}))
			}
			return nil
		}
		result, err := stmt.ExecContext(context.WithValue(p.Ctx, fdwWriteContextKey{}, i), int64(80+i), "current-context")
		r.writer.writeHook = nil
		if err != nil {
			t.Fatal(err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
	if r.writer.modifies-beforeBegins != 2 {
		t.Fatal("cached Execute reused mutable Begin state")
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>=80 ORDER BY id", [][]any{{int64(80), "current-context", true}, {int64(81), "current-context", true}}))
}
