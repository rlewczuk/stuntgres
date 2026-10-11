package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceDatabaseIdentity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			if !backend.PG {
				d := stuntgres.NewDriver()
				first := fixture.DriverPool(t, d, "")
				same := fixture.DriverPool(t, d, "")
				other := fixture.DriverPool(t, d, " ")
				isolated := fixture.DriverPool(t, stuntgres.NewDriver(), "")
				fixture.DriverExecSQL(t, first, "CREATE TABLE public.identity (v INT)")
				fixture.DriverExecSQL(t, first, "INSERT INTO public.identity VALUES (7)")
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{"SELECT v FROM public.identity", "SELECT v FROM identity"} {
					fixture.DriverWantTuples(t, same, q, [][]any{{int64(7)}})
				}
				for _, db := range []*sql.DB{other, isolated} {
					rows, err := db.Query("SELECT v FROM public.identity")
					if rows != nil {
						rows.Close()
						t.Fatal("failed query returned rows")
					}
					fixture.DriverSQLState(t, err, "42P01")
				}
				return
			}
			// PostgreSQL identities are physical sessions in one actual database,
			// not independent embedded Driver registries or opaque DSN keys.
			fixture.DriverExecSQL(t, backend.DB, "CREATE TABLE public.identity (v INT)")
			fixture.DriverExecSQL(t, backend.DB, "INSERT INTO public.identity VALUES (7)")
			first, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			same, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer same.Close()
			var a, b int64
			if err := first.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&a); err != nil {
				t.Fatal(err)
			}
			if err := same.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&b); err != nil {
				t.Fatal(err)
			}
			if a <= 0 || b <= 0 || a == b {
				t.Fatalf("physical identities %d %d", a, b)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"SELECT v FROM public.identity", "SELECT v FROM identity"} {
				fixture.RunStep(t, p.Ctx, same, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

func TestReferencePreparedQueryRevalidatesReplacedCatalog(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE p(v INT)", 0))
			run(fixture.Exec("INSERT INTO p VALUES($1)", 1, fixture.DriverTextValuer("12")))
			q := "SELECT v FROM p WHERE v=$1"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, "12"))
			// Preserve the original non-Context Query/Scan consumer as well.
			rows, err := stmt.Query("12")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var v int64
			if !rows.Next() {
				t.Fatalf("missing held row: %v", rows.Err())
			}
			if err := rows.Scan(&v); err != nil || v != 12 {
				t.Fatalf("held row %d %v", v, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("held exhaustion: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if backend.PG {
				run(fixture.Exec("DROP TABLE p", 0))
			} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("CREATE TABLE p(other TEXT)", 0))
			rows, err = stmt.Query(12)
			if rows != nil {
				rows.Close()
				t.Fatal("revalidation failure exposed rows")
			}
			fixture.DriverSQLState(t, err, "42703")
		})
	}
}

func TestReferenceConcurrentInsertsPreserveSnapshot(t *testing.T) {
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
						result, err := db.ExecContext(p.Ctx, "INSERT INTO concurrent_rows VALUES ($1)", id)
						if err != nil {
							errs <- err
							return
						}
						count, err := result.RowsAffected()
						if err != nil || count != 1 {
							errs <- fmt.Errorf("insert count %d: %v", count, err)
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
				t.Fatalf("snapshot lost row: %v", rows.Err())
			}
			var id int64
			if err := rows.Scan(&id); err != nil || id != 0 {
				t.Fatalf("snapshot row %d %v", id, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("snapshot saw inserts: %v", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			want := make([][]any, 1+workers*perWorker)
			for i := range want {
				want[i] = []any{int64(i)}
			}
			fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Select("SELECT id FROM concurrent_rows ORDER BY id", []string{"id"}, []string{"INT4"}, want))
		})
	}
}
