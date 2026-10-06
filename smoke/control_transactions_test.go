package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func TestControlTransactionsSmoke(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "reset", true: "restore"}[restore]+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				d := stuntgres.NewDriver()
				open := func(driver *stuntgres.Driver, name string) *sql.DB {
					c, err := driver.OpenConnector(name)
					if err != nil {
						t.Fatal(err)
					}
					db := sql.OpenDB(c)
					t.Cleanup(func() { db.Close() })
					return db
				}
				db, peer := open(d, t.Name()), open(d, t.Name())
				x, y := open(d, t.Name()+" "), open(stuntgres.NewDriver(), t.Name())
				exec := func(e transactionExecutor, q string, n int64) {
					t.Helper()
					if err := transactionAffected(ctx, e, q, prepared, n); err != nil {
						t.Fatal(err)
					}
				}
				values := func(e transactionExecutor, want ...int64) {
					t.Helper()
					if err := transactionSelectValues(ctx, e, "SELECT v FROM t ORDER BY v", false, want...); err != nil {
						t.Fatal(err)
					}
				}
				ordinary := func(err error) {
					t.Helper()
					if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
						t.Fatal("replacement boundary", err)
					}
					var diagnostic *stuntgres.Error
					if errors.As(err, &diagnostic) {
						t.Fatal("fabricated SQLSTATE", diagnostic)
					}
				}
				for _, e := range []*sql.DB{peer, x, y} {
					exec(e, "CREATE TABLE t(v INT NOT NULL)", 0)
				}
				exec(peer, "INSERT INTO t VALUES(77)", 1)
				exec(x, "INSERT INTO t VALUES(22)", 1)
				exec(y, "INSERT INTO t VALUES(33)", 1)
				a, err := db.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer a.Close()
				defer a.ExecContext(context.Background(), "ROLLBACK")
				exec(a, "BEGIN", 0)
				exec(a, "INSERT INTO t VALUES(1)", 1)
				values(a, 1, 77)
				values(peer, 77)
				var held *sql.Stmt
				if prepared {
					held, err = a.PrepareContext(ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
				}
				end, err := a.PrepareContext(ctx, "COMMIT AND CHAIN")
				if err != nil {
					t.Fatal(err)
				}
				defer end.Close()
				cursor, err := peer.QueryContext(ctx, "SELECT v FROM t")
				if err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				if restore {
					err = stuntgres.Restore(ctx, peer, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99');"))
				} else {
					err = stuntgres.Reset(ctx, peer)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err = transactionValues(cursor, 77); err != nil {
					t.Fatal(err)
				}
				if held != nil {
					var rows *sql.Rows
					rows, err = held.QueryContext(ctx)
					if rows != nil {
						rows.Close()
					}
				} else {
					_, err = a.ExecContext(ctx, "SELECT v FROM t")
				}
				ordinary(err)
				if prepared {
					_, err = end.ExecContext(ctx)
				} else {
					_, err = a.ExecContext(ctx, "COMMIT AND CHAIN")
				}
				ordinary(err)
				if !restore {
					_, err = peer.QueryContext(ctx, "SELECT v FROM t")
					if err = transactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}); err != nil {
						t.Fatal(err)
					}
					exec(peer, "CREATE TABLE t(replacement TEXT)", 0)
				}
				exec(a, "INSERT INTO t VALUES('2')", 1)
				rows, err := peer.QueryContext(ctx, "SELECT replacement FROM t ORDER BY replacement")
				if err != nil {
					t.Fatal(err)
				}
				ct, err := rows.ColumnTypes()
				if err != nil || len(ct) != 1 || ct[0].Name() != "replacement" || ct[0].DatabaseTypeName() != "TEXT" {
					t.Fatal(ct, err)
				}
				want := []string{"2"}
				if restore {
					want = append(want, "99")
				}
				for _, v := range want {
					if !rows.Next() {
						t.Fatal("missing new row", rows.Err())
					}
					var got string
					if err = rows.Scan(&got); err != nil || got != v {
						t.Fatal(got, err)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("resurrected rows", rows.Err())
				}
				rows.Close()
				values(x, 22)
				values(y, 33)
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("cancel_during_read_and_borrower_release/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d := stuntgres.NewDriver()
			c, err := d.OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db, peer := sql.OpenDB(c), sql.OpenDB(c)
			defer db.Close()
			defer peer.Close()
			peer.SetMaxOpenConns(1)
			exec := func(e transactionExecutor, q string, n int64) {
				t.Helper()
				if err := transactionAffected(ctx, e, q, prepared, n); err != nil {
					t.Fatal(err)
				}
			}
			exec(peer, "CREATE TABLE t(v INT)", 0)
			exec(peer, "INSERT INTO t VALUES(77)", 1)
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			defer a.ExecContext(context.Background(), "ROLLBACK")
			exec(a, "BEGIN", 0)
			exec(a, "INSERT INTO t VALUES(1)", 1)
			r, w := io.Pipe()
			defer r.Close()
			defer w.Close()
			canceled, stop := context.WithCancel(ctx)
			defer stop()
			entered := make(chan struct{})
			done := make(chan error, 1)
			go func() { done <- stuntgres.Restore(canceled, peer, &controlSmokeReader{Reader: r, entered: entered}) }()
			joined := false
			defer func() {
				w.Close()
				if !joined {
					<-done
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err = transactionSelectValues(ctx, peer, "SELECT v FROM t", false, 77); err != nil {
				t.Fatal(err)
			}
			exec(peer, "INSERT INTO t VALUES(88)", 1)
			stop()
			w.Close()
			err = <-done
			joined = true
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			exec(a, "COMMIT", 0)
			if err = transactionSelectValues(ctx, peer, "SELECT v FROM t ORDER BY v", false, 1, 77, 88); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type controlSmokeReader struct {
	io.Reader
	entered chan struct{}
}

func (r *controlSmokeReader) Read(p []byte) (int, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
	}
	return r.Reader.Read(p)
}
