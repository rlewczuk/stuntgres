package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceUUIDOsspTimeConcurrency(t *testing.T) {
	uuidOsspCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		var db *sql.DB
		for _, backend := range p.Backends() {
			if backend.PG == pg {
				db = backend.DB
			}
		}
		if db == nil {
			t.Fatal("missing owning backend")
		}
		seen := map[string]bool{}
		for _, name := range uuidOsspTimeNames {
			for _, prepared := range []bool{false, true} {
				label := "physical_sessions"
				if prepared {
					label = "shared_db_statement"
				}
				t.Run(name+"/"+label, func(t *testing.T) {
					const workers, demands = 8, 32
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					defer cancel()
					q := "SELECT " + name + "() AS u"
					var stmt *sql.Stmt
					var conns []*sql.Conn
					if prepared {
						var err error
						stmt, err = db.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					} else {
						defer func() {
							for _, conn := range conns {
								conn.Close()
							}
						}()
						for range workers {
							conn, err := db.Conn(ctx)
							if err != nil {
								t.Fatal(err)
							}
							conns = append(conns, conn)
						}
					}
					type result struct {
						values []string
						err    error
					}
					results := make(chan result, workers)
					start := make(chan struct{})
					var wg sync.WaitGroup
					before := time.Now()
					for worker := range workers {
						wg.Add(1)
						go func(worker int) {
							defer wg.Done()
							<-start
							out := result{values: make([]string, 0, demands)}
							for demand := range demands {
								var value string
								var err error
								if prepared {
									err = stmt.QueryRowContext(ctx).Scan(&value)
								} else {
									err = conns[worker].QueryRowContext(ctx, q).Scan(&value)
								}
								if err != nil {
									out.err = fmt.Errorf("worker %d demand %d: %w", worker, demand, err)
									break
								}
								out.values = append(out.values, value)
							}
							results <- out
						}(worker)
					}
					// Cancel and join before rows/statements/connections or fixture teardown,
					// including assertion failures. Buffered results never block worker exit.
					defer func() { cancel(); wg.Wait() }()
					close(start)
					var values []string
					var failures []error
					for range workers {
						select {
						case out := <-results:
							values = append(values, out.values...)
							if out.err != nil {
								failures = append(failures, out.err)
								cancel()
							}
						case <-ctx.Done():
							t.Fatalf("concurrent collection: %v", ctx.Err())
						}
					}
					wg.Wait()
					after := time.Now()
					if len(failures) != 0 || len(values) != workers*demands {
						t.Fatalf("count=%d want=256 errors=%v", len(values), failures)
					}
					for _, value := range values {
						f := uuidOsspTimeDecode(t, value, name == "uuid_generate_v1mc")
						if f.instant.Before(before.Add(-2*time.Second)) || f.instant.After(after.Add(2*time.Second)) {
							t.Fatal("concurrent UUID outside demand bracket", f.instant)
						}
					}
					uuidOsspTimeUnique(t, seen, values)
					// No timestamp ordering is inferred from worker completion order.
				})
			}
		}
		if len(seen) != 1024 {
			t.Fatal("combined unique bag", len(seen))
		}
	})
}
