package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWConcurrency(t *testing.T) {
	for _, phase := range []string{"scan-reset", "validator-reset", "import-reset", "validator-revoke", "validator-drop", "source-query-cancel"} {
		t.Run(phase, func(t *testing.T) {
			fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, r, run)
				run(fixture.Select("SELECT id FROM fdw_data WHERE id=1", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if b.PG {
					return
				}
				if phase == "source-query-cancel" {
					blocker, err := r.source.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer blocker.Close()
					tx, err := blocker.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, nil, fixture.Exec("LOCK TABLE fdw_source.data IN ACCESS EXCLUSIVE MODE", 0))
					before := r.snapshot()
					query := "SELECT id FROM fdw_data"
					// The existing helper observes the actual source SQL lock
					// waiter in the worker's live stack, then cancels and joins.
					fixture.RelationLockActiveCancel(t, p, b, c, query, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowQuery(ctx, c, query, false, b.Recorder.RawBackend)
					})
					fdwReleased(t, r, before, 0)
					return
				}
				method, query := "IterateForeignScan", "SELECT id FROM fdw_data"
				switch phase {
				case "validator-reset":
					method, query = "Validate", "ALTER FOREIGN TABLE fdw_data OPTIONS(SET table_name 'data')"
				case "import-reset":
					run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
					method, query = "ImportForeignSchema", "IMPORT FOREIGN SCHEMA fdw_source LIMIT TO (data) FROM SERVER fdw_live INTO fdw_import"
				case "validator-revoke":
					run(fixture.Exec("CREATE ROLE fdw_reader", 0))
					run(fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_native TO fdw_reader", 0))
					run(fixture.Exec("SET ROLE fdw_reader", 0))
					method, query = "Validate", "CREATE SERVER fdw_staged FOREIGN DATA WRAPPER fdw_native"
				case "validator-drop":
					run(fixture.Exec("CREATE ROLE fdw_reader", 0))
					method, query = "Validate", "CREATE USER MAPPING FOR fdw_reader SERVER fdw_live OPTIONS(user 'postgres')"
				}
				entered, release := make(chan struct{}), make(chan struct{})
				done := make(chan error, 1)
				var gate sync.Once
				r.setHook(func(ctx context.Context, at string) error {
					if at == method {
						gate.Do(func() {
							close(entered)
							select {
							case <-release:
							case <-ctx.Done():
							}
						})
					}
					return ctx.Err()
				})
				joined, released := false, false
				defer func() {
					if !released {
						close(release)
					}
					if !joined {
						<-done
					}
					r.setHook(nil)
				}()
				before := r.snapshot()
				go func() {
					var err error
					if phase == "scan-reset" {
						var rows *sql.Rows
						rows, err = c.QueryContext(p.Ctx, query)
						if rows != nil {
							rows.Close()
							if err == nil {
								err = errors.New("replaced eager query published rows")
							}
						}
					} else {
						_, err = c.ExecContext(p.Ctx, query)
					}
					done <- err
				}()
				select {
				case <-entered:
				case err := <-done:
					joined = true
					t.Fatalf("execution ended before callback gate: %v", err)
				case <-p.Ctx.Done():
					t.Fatal(p.Ctx.Err())
				}
				// This actual peer query requires engine/catalog admission. It must
				// finish before the suspended application callback is released.
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) FROM pg_foreign_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				want := "stuntgres: catalog replaced before publication"
				if phase == "validator-revoke" {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdw_native FROM fdw_reader", 0))
					want = "stuntgres: catalog publication conflict"
				} else if phase == "validator-drop" {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP ROLE fdw_reader", 0))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) FROM pg_roles WHERE rolname='fdw_reader'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				close(release)
				released = true
				err := <-done
				joined = true
				if phase == "validator-drop" {
					var record *stuntgres.Error
					if err == nil || errors.As(err, &record) {
						t.Fatal("dropped captured principal was published or acquired a fabricated SQL error", err)
					}
				} else {
					fixture.TransactionOrdinary(t, err, want)
				}
				r.setHook(nil)
				owned := 0
				if phase == "scan-reset" {
					owned = 1
				}
				fdwReleased(t, r, before, owned)
				if phase == "validator-drop" {
					run(fixture.Select("SELECT count(*) FROM pg_user_mapping", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
				} else if phase == "validator-revoke" {
					run(fixture.Exec("RESET ROLE", 0))
					run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_staged'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				} else {
					run(fixture.Select("SELECT count(*) FROM pg_foreign_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		})
	}
}
