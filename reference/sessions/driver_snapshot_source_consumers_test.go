package reference_test

import (
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferencePreparedQueryRevalidatesReplacedCatalogSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.DriverExecSQL(t, db, "CREATE TABLE p(v INT)")
			fixture.DriverExecSQL(t, db, "INSERT INTO p VALUES($1)", fixture.DriverTextValuer("12"))
			stmt, err := db.Prepare("SELECT v FROM p WHERE v=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.Query("12")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			fixture.DriverBasicMetadata(t, rows, backend.PG, []string{"INT4"}, []bool{true})
			if !rows.Next() {
				t.Fatalf("missing prepared row: %v", rows.Err())
			}
			var v int64
			if err := rows.Scan(&v); err != nil || v != 12 {
				t.Fatalf("prepared row %d: %v", v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("prepared exhaustion: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if backend.PG {
				fixture.DriverExecSQL(t, db, "DROP TABLE p")
			} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.DriverExecSQL(t, db, "CREATE TABLE p(other TEXT)")
			rows, err = stmt.Query(12)
			if rows != nil {
				rows.Close()
				t.Fatal("failed revalidation exposed rows")
			}
			fixture.DriverSQLState(t, err, "42703")
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceConcurrentInsertsPreserveSnapshotSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			fixture.DriverExecSQL(t, db, "CREATE TABLE concurrent_rows (id INTEGER NOT NULL)")
			fixture.DriverExecSQL(t, db, "INSERT INTO concurrent_rows VALUES (0)")
			rows, err := db.Query("SELECT id FROM concurrent_rows")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			const workers, perWorker = 8, 32
			start := make(chan struct{})
			errs := make(chan error, workers)
			var wg sync.WaitGroup
			for worker := range workers {
				wg.Go(func() {
					<-start
					for i := range perWorker {
						id := 1 + worker*perWorker + i
						result, err := db.Exec("INSERT INTO concurrent_rows VALUES ($1)", id)
						if err != nil {
							errs <- err
							return
						}
						count, err := result.RowsAffected()
						if err != nil || count != 1 {
							errs <- fmt.Errorf("insert count %d: %v", count, err)
							return
						}
						if _, err := result.LastInsertId(); err == nil {
							errs <- fmt.Errorf("LastInsertId unexpectedly supported")
							return
						}
					}
				})
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatalf("snapshot lost original row: %v", rows.Err())
			}
			var id int64
			if err := rows.Scan(&id); err != nil || id != 0 {
				t.Fatalf("snapshot row %d: %v", id, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("snapshot saw later writes: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			want := make([][]any, 1+workers*perWorker)
			for i := range want {
				want[i] = []any{int64(i)}
			}
			fixture.DriverWantTuples(t, db, "SELECT id FROM concurrent_rows ORDER BY id", want)
		})
	}
}
