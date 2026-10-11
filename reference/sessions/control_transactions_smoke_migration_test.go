package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceControlTransactionsSmoke(t *testing.T) {
	for _, restore := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "reset", true: "restore"}[restore]+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						ctx = context.WithValue(ctx, fixture.RecorderKey{}, backend.Recorder)
						m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: backend.Recorder}
						d := p.NativeConnector.Driver().(*stuntgres.Driver)
						open := func(driver *stuntgres.Driver, name string) *sql.DB {
							c, err := driver.OpenConnector(name)
							if err != nil {
								t.Fatal(err)
							}
							db := sql.OpenDB(fixture.MigrationRecordedConnector(t, c))
							t.Cleanup(func() { db.Close() })
							return db
						}
						db := backend.DB
						var noticeRecords []fixture.Diagnostic
						peer, borrower, _ := fixture.NoticeReferencePool(t, p, backend.PG, &noticeRecords)
						if err := borrower.Close(); err != nil {
							t.Fatal(err)
						}
						var x, y *sql.DB
						if !backend.PG {
							x, y = open(d, "reference/"+t.Name()+" "), open(stuntgres.NewDriver(), "reference/"+t.Name())
						}
						exec := func(e fixture.Executor, q string, n int64) {
							t.Helper()
							if err := m.TransactionAffected(ctx, e, q, prepared, n); err != nil {
								t.Fatal(err)
							}
						}
						values := func(e fixture.Executor, want ...int64) {
							t.Helper()
							if err := m.TransactionSelectValues(ctx, e, "SELECT v FROM t ORDER BY v", false, want...); err != nil {
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
						pools := []*sql.DB{peer}
						if !backend.PG {
							pools = append(pools, x, y)
						}
						for _, e := range pools {
							exec(e, "CREATE TABLE t(v INT NOT NULL)", 0)
						}
						exec(peer, "INSERT INTO t VALUES(77)", 1)
						if !backend.PG {
							exec(x, "INSERT INTO t VALUES(22)", 1)
							exec(y, "INSERT INTO t VALUES(33)", 1)
						}
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
							held, err = a.PrepareContext(ctx, "SELECT v FROM t ORDER BY v")
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
						// Reset/Restore replacement and incarnation errors are native APIs.
						// PG independently exercises the same private SQL and held COMMIT
						// routes, without inventing a catalog replacement operation.
						if backend.PG {
							if err := m.TransactionValues(cursor, 77); err != nil {
								t.Fatal(err)
							}
							if held != nil {
								r, e := held.QueryContext(ctx)
								if e != nil {
									t.Fatal(e)
								}
								if e = m.TransactionValues(r, 1, 77); e != nil {
									t.Fatal(e)
								}
							}
							if prepared {
								_, err = end.ExecContext(ctx)
							} else {
								_, err = a.ExecContext(ctx, "COMMIT AND CHAIN")
							}
							if err != nil {
								t.Fatal(err)
							}
							values(peer, 1, 77)
							exec(a, "ROLLBACK", 0)
							return
						}
						if restore {
							err = stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('99');"))
						} else {
							err = stuntgres.Reset(ctx, p.Native)
						}
						if err != nil {
							t.Fatal(err)
						}
						if err = m.TransactionValues(cursor, 77); err != nil {
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
							if err = m.TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}); err != nil {
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
			})
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run("cancel_during_read_and_borrower_release/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			p := fixture.NewFixture(t, false, true)
			var noticeRecords []fixture.Diagnostic
			db := p.Memory
			_, borrower, recorder := fixture.NoticeReferencePool(t, p, false, &noticeRecords)
			if err := borrower.Close(); err != nil {
				t.Fatal(err)
			}
			peer := p.Native
			ctx = context.WithValue(ctx, fixture.RecorderKey{}, recorder)
			m := fixture.MigrationSmoke{T: t, Native: p.Native, Recorder: recorder}
			peer.SetMaxOpenConns(1)
			exec := func(e fixture.Executor, q string, n int64) {
				t.Helper()
				if err := m.TransactionAffected(ctx, e, q, prepared, n); err != nil {
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
			go func() {
				done <- stuntgres.Restore(canceled, p.Native, &migrationControlReader{Reader: r, entered: entered})
			}()
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
			if err = m.TransactionSelectValues(ctx, peer, "SELECT v FROM t", false, 77); err != nil {
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
			if err = m.TransactionSelectValues(ctx, peer, "SELECT v FROM t ORDER BY v", false, 1, 77, 88); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type migrationControlReader struct {
	io.Reader
	entered chan struct{}
}

func (r *migrationControlReader) Read(p []byte) (int, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
	}
	return r.Reader.Read(p)
}
