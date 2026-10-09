package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestReferenceNativeCursorEOFAndCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			pool := backend.db
			if !backend.pg {
				pool = p.native
			}
			conn, err := pool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			err = conn.Raw(func(raw any) error {
				ex := raw.(driver.ExecerContext)
				q := raw.(driver.QueryerContext)
				for _, query := range []string{"CREATE TABLE c(v INT)", "INSERT INTO c VALUES(1)"} {
					if _, err := ex.ExecContext(p.ctx, query, nil); err != nil {
						return err
					}
				}
				rows, err := q.QueryContext(p.ctx, "SELECT v FROM c", nil)
				if err != nil {
					return err
				}
				defer rows.Close()
				columns := rows.Columns()
				if !reflect.DeepEqual(columns, []string{"v"}) {
					t.Fatalf("raw columns %#v", columns)
				}
				columns[0] = "detached"
				if !backend.pg && !reflect.DeepEqual(rows.Columns(), []string{"v"}) {
					t.Fatal("columns alias cursor storage")
				}
				dest := make([]driver.Value, 1)
				if err := rows.Next(dest); err != nil || dest[0] != int64(1) {
					t.Fatalf("raw row %#v %v", dest, err)
				}
				if err := rows.Next(dest); err != io.EOF {
					t.Fatalf("raw EOF %v", err)
				}
				if err := rows.Close(); err != nil {
					return err
				}
				if err := rows.Next(dest); err != io.EOF {
					t.Fatalf("sticky EOF %v", err)
				}
				if !backend.pg {
					ctx, cancel := context.WithCancel(p.ctx)
					defer cancel()
					rows, err := q.QueryContext(ctx, "SELECT v FROM c", nil)
					if err != nil {
						return err
					}
					defer rows.Close()
					cancel()
					driverOrdinaryError(t, rows.Next(dest), context.Canceled)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if backend.pg {
				referenceSessionCancel(t, p.ctx, conn, false)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM c", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceDriverCanceledHandles(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if backend.pg {
				// A pre-canceled fast pq request can reach the server before its
				// transport cancellation goroutine. Exercise actual admission
				// and server cancellation instead of accepting either outcome.
				var pid int
				if err := conn.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
					t.Fatal(err)
				}
				if pid <= 0 {
					t.Fatalf("invalid backend identity %d", pid)
				}
				if _, err := conn.ExecContext(p.ctx, "SET application_name='driver_cancel'"); err != nil {
					t.Fatal(err)
				}
				const query = "SELECT 1"
				stmt, err := conn.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: query}
				for _, prepared := range []bool{false, true} {
					referenceSessionCancel(t, p.ctx, conn, prepared)
					var after int
					var setting string
					if err := conn.QueryRowContext(p.ctx, "SELECT pg_backend_pid(),current_setting('application_name')").Scan(&after, &setting); err != nil {
						t.Fatal(err)
					}
					if after != pid || setting != "driver_cancel" {
						t.Fatalf("cancellation changed owner %d/%d setting %q", pid, after, setting)
					}
					referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(query, []string{"?column?"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if err := conn.Close(); err != nil {
					t.Fatal(err)
				}
				rows, err := conn.QueryContext(p.ctx, query)
				if rows != nil {
					rows.Close()
					t.Fatal("closed borrower exposed rows")
				}
				driverOrdinaryError(t, err, sql.ErrConnDone)
				return
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			stmt, err := conn.PrepareContext(ctx, "SELECT 1")
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			rows, err := conn.QueryContext(ctx, "SELECT 1")
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
