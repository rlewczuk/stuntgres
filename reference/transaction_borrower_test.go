package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
)

func TestReferenceTransactionBorrowerCloseAndPhysicalEnd(t *testing.T) {
	p := newReferenceScenario(t)
	nativeDSN := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			db := p.native
			if backend.pg {
				connector, err := pq.NewConnector(referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				db = sql.OpenDB(connector)
				t.Cleanup(func() { db.Close() })
			}
			db.SetMaxOpenConns(1)
			db.SetMaxIdleConns(1)
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.Close() })
			var physical driver.Conn
			var handle driver.Tx
			if err := a.Raw(func(raw any) error {
				physical = raw.(driver.Conn)
				referenceTransactionRawExec(t, ctx, physical, "CREATE TABLE t(v INT)", 0)
				referenceTransactionRawExec(t, ctx, physical, "INSERT INTO t VALUES(7)", 1)
				var err error
				handle, err = physical.Begin()
				if err == nil {
					referenceTransactionRawExec(t, ctx, physical, "INSERT INTO t VALUES(1)", 1)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			borrower, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer borrower.Close()
			if err := borrower.Raw(func(raw any) error {
				if raw != physical {
					t.Fatal("borrower changed physical handle")
				}
				referenceTransactionRawValues(t, physical, "SELECT v FROM t ORDER BY v", 1, 7)
				return handle.Rollback()
			}); err != nil {
				t.Fatal(err)
			}
			if err := borrower.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Raw(func(raw any) error {
				c := raw.(driver.Conn)
				_, err := c.Begin()
				if err == nil {
					referenceTransactionRawExec(t, ctx, c, "INSERT INTO t VALUES(2)", 1)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, ctx, backend.db, backend.recorder, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 7))
			if !backend.pg {
				reopened := sql.OpenDB(p.nativeConnector)
				t.Cleanup(func() { reopened.Close() })
				raw, _ := referenceTransactionRaw(t, p, backend)
				referenceTransactionRawValues(t, raw, "SELECT v FROM t ORDER BY v", 7)
				rows, err := reopened.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				referenceLimitLockValues(t, relationOutcome{rows: rows}, false, 7)
				for _, cohort := range []struct {
					driver *stuntgres.Driver
					dsn    string
				}{{p.nativeConnector.Driver().(*stuntgres.Driver), t.Name() + "/other"}, {stuntgres.NewDriver(), nativeDSN}} {
					connector, err := cohort.driver.OpenConnector(cohort.dsn)
					if err != nil {
						t.Fatal(err)
					}
					other := sql.OpenDB(connector)
					t.Cleanup(func() { other.Close() })
					rows, err := other.QueryContext(ctx, "SELECT v FROM t")
					if rows != nil {
						rows.Close()
						t.Fatal("isolated store returned a cursor")
					}
					referenceState(t, err, "42P01")
				}
			} else {
				connector, err := pq.NewConnector(referenceDSN)
				if err != nil {
					t.Fatal(err)
				}
				fresh := sql.OpenDB(connector)
				t.Cleanup(func() { fresh.Close() })
				rows, err := fresh.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				referenceLimitLockValues(t, relationOutcome{rows: rows, err: err}, true, 7)
			}
		})
	}
}

func TestReferenceTransactionRawTerminalCleanup(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := p.ctx
			a, closeA := referenceTransactionRaw(t, p, backend)
			b, _ := referenceTransactionRaw(t, p, backend)
			referenceTransactionRawExec(t, ctx, b, "CREATE TABLE t(v INT)", 0)
			referenceTransactionRawExec(t, ctx, b, "INSERT INTO t VALUES(7)", 1)
			referenceTransactionRawExec(t, ctx, b, "SET application_name TO peer", 0)
			if _, err := a.Begin(); err != nil {
				t.Fatal(err)
			}
			referenceTransactionRawExec(t, ctx, a, "INSERT INTO t VALUES(1)", 1)
			if err := closeA(); err != nil {
				t.Fatal(err)
			}
			referenceTransactionRawValues(t, b, "SELECT v FROM t ORDER BY v", 7)
			rows, err := b.(driver.QueryerContext).QueryContext(context.Background(), "SHOW application_name", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			value := make([]driver.Value, 1)
			if err := rows.Next(value); err != nil || value[0] != "peer" {
				t.Fatal(value, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
