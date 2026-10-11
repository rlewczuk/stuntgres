package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCancellationTransactionsAndClosedObjects(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.DriverExecSQL(t, db, "CREATE TABLE c (v INT)")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := db.ExecContext(ctx, "INSERT INTO c VALUES (1)"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			fixture.DriverWantTuples(t, db, "SELECT v FROM c", [][]any{})
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Exec("SELECT v FROM c WINDOW w AS ()", 0))
			for _, q := range []string{"SAVEPOINT x", "RELEASE x"} {
				_, err = db.Exec(q)
				fixture.DriverSQLState(t, err, "25P01")
			}
			stmt, err := db.Prepare("INSERT INTO c VALUES ($1)")
			if err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			stmt.Close()
			if _, err = stmt.Exec(1); err == nil {
				t.Fatal("closed statement accepted execution")
			}
			if backend.PG {
				return
			}
			conn, err := stuntgres.NewDriver().Open("native")
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			conn.Close()
			if _, err = conn.Prepare("SELECT v FROM c"); err == nil {
				t.Fatal("closed connection prepared")
			}
			if err = conn.(driver.Pinger).Ping(context.Background()); err == nil {
				t.Fatal("closed connection pinged")
			}
		})
	}
}
