package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWriteProviderContractErrors(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader) {
	t.Helper()
	id := 100
	for _, sentinel := range []error{driver.ErrBadConn, driver.ErrSkip} {
		for _, injected := range []error{sentinel, fmt.Errorf("provider transport: %w", sentinel), errors.Join(errors.New("provider joined failure"), sentinel)} {
			for _, prepared := range []bool{false, true} {
				for _, phase := range []string{"AfterWrite", "EndForeignModify", "EndForeignScan", "AfterCommit"} {
					id++
					q := fmt.Sprintf("INSERT INTO fdw_data VALUES(%d,'contract',true)", id)
					// This source is demanded only by RETURNING, after the
					// actual INSERT, so its End fault is genuinely post-write.
					if phase == "EndForeignScan" {
						q = fmt.Sprintf("INSERT INTO fdw_data VALUES(%d,'contract',true) RETURNING (SELECT label FROM fdw_data WHERE id=1)", id)
					}
					calls := 0
					rollbacks := r.writer.rollbacks
					r.writer.writeHook = func(ctx context.Context, name string) error {
						if name == phase {
							calls++
							return injected
						}
						return nil
					}
					var result sql.Result
					var err error
					if prepared {
						stmt, prepareErr := c.PrepareContext(p.Ctx, q)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						result, err = stmt.ExecContext(p.Ctx)
						if closeErr := stmt.Close(); closeErr != nil {
							t.Fatal(closeErr)
						}
					} else {
						result, err = c.ExecContext(p.Ctx, q)
					}
					r.writer.writeHook = nil
					if err == nil || result != nil || calls != 1 || errors.Is(err, driver.ErrBadConn) || errors.Is(err, driver.ErrSkip) || !strings.Contains(err.Error(), injected.Error()) {
						t.Fatalf("phase=%s prepared=%t callbacks=%d result=%v err=%v", phase, prepared, calls, result, err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("provider contract error invented SQLSTATE", err)
					}
					var want [][]any
					if phase == "AfterCommit" {
						want = [][]any{{int64(id), "contract", true}}
						if r.writer.rollbacks != rollbacks {
							t.Fatal("acknowledged/unknown committed provider was rolled back")
						}
					}
					fdwWriteSourceRows(t, p, b, r, fdwRows(fmt.Sprintf("SELECT id,label,flag FROM fdw_source.data WHERE id=%d", id), want))
				}
			}
		}
	}
}
