package reference_test

import (
	"database/sql"
	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionControlsSessionLifecycle(t *testing.T) {
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
			peer := backend.DB
			fixture.RunStep(t, ctx, peer, backend.Recorder, fixture.Exec("CREATE TABLE t(v INT NOT NULL)", 0))
			fixture.RunStep(t, ctx, peer, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(77)", 1))
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { a.Close() })
			exec := func(q string, n int64) { fixture.LockExec(t, ctx, a, q, n) }
			values := func(want ...int64) {
				rows, err := a.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, backend.PG, want...)
			}
			exec("SET application_name TO 'sql-owner'", 0)
			exec("BEGIN", 0)
			exec("INSERT INTO t VALUES(1)", 1)
			stmt, err := a.PrepareContext(ctx, "COMMIT")
			if err != nil {
				t.Fatal(err)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			var owner any
			if err := a.Raw(func(raw any) error { owner = raw; return nil }); err != nil {
				t.Fatal(err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Raw(func(raw any) error {
				if raw != owner {
					t.Fatal("SQL owner changed physical handle")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			values(1, 77)
			fixture.RunStep(t, ctx, peer, backend.Recorder, fixture.TransactionRows(false, "SELECT v FROM t", 77))
			exec("ROLLBACK", 0)
			var name string
			if err := a.QueryRowContext(ctx, "SHOW application_name").Scan(&name); err != nil || name != "sql-owner" {
				t.Fatal(name, err)
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, err = db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			exec("INSERT INTO t VALUES(2)", 1)
			fixture.RunStep(t, ctx, peer, backend.Recorder, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 2, 77))
			exec("BEGIN", 0)
			exec("INSERT INTO t VALUES(3)", 1)
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, ctx, peer, backend.Recorder, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 2, 77))
			if !backend.PG {
				reopened := sql.OpenDB(p.NativeConnector)
				t.Cleanup(func() { reopened.Close() })
				rows, err := reopened.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, false, 2, 77)
				for _, cohort := range []struct {
					d   *stuntgres.Driver
					dsn string
				}{{p.NativeConnector.Driver().(*stuntgres.Driver), t.Name() + "/other"}, {stuntgres.NewDriver(), nativeDSN}} {
					connector, err := cohort.d.OpenConnector(cohort.dsn)
					if err != nil {
						t.Fatal(err)
					}
					isolated := sql.OpenDB(connector)
					t.Cleanup(func() { isolated.Close() })
					fixture.LockExec(t, ctx, isolated, "CREATE TABLE t(v INT)", 0)
					rows, err := isolated.QueryContext(ctx, "SELECT v FROM t")
					fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, false)
				}
			} else {
				connector, err := pq.NewConnector(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				fresh := sql.OpenDB(connector)
				t.Cleanup(func() { fresh.Close() })
				rows, err := fresh.QueryContext(ctx, "SELECT v FROM t ORDER BY v")
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, true, 2, 77)
			}
		})
	}
}
