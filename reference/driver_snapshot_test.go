package reference_test

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"stuntgres"
)

func TestReferenceDatabaseIdentity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			if !backend.pg {
				d := stuntgres.NewDriver()
				first := driverPool(t, d, "")
				same := driverPool(t, d, "")
				other := driverPool(t, d, " ")
				isolated := driverPool(t, stuntgres.NewDriver(), "")
				driverExecSQL(t, first, "CREATE TABLE public.identity (v INT)")
				driverExecSQL(t, first, "INSERT INTO public.identity VALUES (7)")
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{"SELECT v FROM public.identity", "SELECT v FROM identity"} {
					driverWantTuples(t, same, q, [][]any{{int64(7)}})
				}
				for _, db := range []*sql.DB{other, isolated} {
					rows, err := db.Query("SELECT v FROM public.identity")
					if rows != nil {
						rows.Close()
						t.Fatal("failed query returned rows")
					}
					driverSQLState(t, err, "42P01")
				}
				return
			}
			// PostgreSQL identities are physical sessions in one actual database,
			// not independent embedded Driver registries or opaque DSN keys.
			driverExecSQL(t, backend.db, "CREATE TABLE public.identity (v INT)")
			driverExecSQL(t, backend.db, "INSERT INTO public.identity VALUES (7)")
			first, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Close()
			same, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer same.Close()
			var a, b int64
			if err := first.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&a); err != nil {
				t.Fatal(err)
			}
			if err := same.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&b); err != nil {
				t.Fatal(err)
			}
			if a <= 0 || b <= 0 || a == b {
				t.Fatalf("physical identities %d %d", a, b)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{"SELECT v FROM public.identity", "SELECT v FROM identity"} {
				referenceRunStep(t, p.ctx, same, backend.recorder, referenceSelect(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

func TestReferencePreparedQueryRevalidatesReplacedCatalog(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, backend.recorder, s) }
			run(referenceExec("CREATE TABLE p(v INT)", 0))
			run(referenceExec("INSERT INTO p VALUES($1)", 1, driverTextValuer("12")))
			q := "SELECT v FROM p WHERE v=$1"
			stmt, err := conn.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
			referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, "12"))
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
			if backend.pg {
				run(referenceExec("DROP TABLE p", 0))
			} else if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("CREATE TABLE p(other TEXT)", 0))
			rows, err = stmt.Query(12)
			if rows != nil {
				rows.Close()
				t.Fatal("revalidation failure exposed rows")
			}
			driverSQLState(t, err, "42703")
		})
	}
}

func TestReferenceConcurrentInsertsPreserveSnapshot(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			driverExecSQL(t, db, "CREATE TABLE concurrent_rows (id INTEGER NOT NULL)")
			driverExecSQL(t, db, "INSERT INTO concurrent_rows VALUES (0)")
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
						result, err := db.ExecContext(p.ctx, "INSERT INTO concurrent_rows VALUES ($1)", id)
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
			referenceRunStep(t, p.ctx, db, backend.recorder, referenceSelect("SELECT id FROM concurrent_rows ORDER BY id", []string{"id"}, []string{"INT4"}, want))
		})
	}
}
