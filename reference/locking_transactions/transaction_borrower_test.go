package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionBorrowerCloseAndPhysicalEnd(t *testing.T) {
	p := fixture.NewScenario(t)
	nativeDSN := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			db := p.Native
			if backend.PG {
				connector, err := pq.NewConnector(fixture.DSN)
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
			fixture.RunStep(t, ctx, backend.DB, backend.Recorder, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 7))
			if !backend.PG {
				reopened := sql.OpenDB(p.NativeConnector)
				t.Cleanup(func() { reopened.Close() })
				raw, _ := referenceTransactionRaw(t, p, backend)
				referenceTransactionRawValues(t, raw, "SELECT v FROM t ORDER BY v", 7)
				rows, err := reopened.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, false, 7)
				for _, cohort := range []struct {
					driver *stuntgres.Driver
					dsn    string
				}{{p.NativeConnector.Driver().(*stuntgres.Driver), t.Name() + "/other"}, {stuntgres.NewDriver(), nativeDSN}} {
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
					fixture.State(t, err, "42P01")
				}
			} else {
				connector, err := pq.NewConnector(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				fresh := sql.OpenDB(connector)
				t.Cleanup(func() { fresh.Close() })
				rows, err := fresh.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, true, 7)
			}
		})
	}
}

func TestReferenceTransactionRawTerminalCleanup(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
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
