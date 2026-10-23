package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceSnapshotRelationAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			name := "direct/" + completion
			if prepared {
				name = "held/" + completion
			}
			t.Run(name, func(t *testing.T) {
				snapshotRemedyScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, bumper, reader, observer := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
					fixture.LockExec(t, p.Ctx, bumper, "CREATE TABLE snapshot_relation_rows(id integer)", 0)
					fixture.LockExec(t, p.Ctx, bumper, "INSERT INTO snapshot_relation_rows VALUES(1)", 1)
					var oid int64
					if err := observer.QueryRowContext(p.Ctx, "SELECT 'snapshot_relation_rows'::regclass::oid::bigint").Scan(&oid); err != nil {
						t.Fatal(err)
					}
					const heldQuery = "SELECT id,pg_current_snapshot()::text AS s,pg_visible_in_snapshot($1::xid8,pg_current_snapshot()) AS visible FROM snapshot_relation_rows ORDER BY id"
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = reader.PrepareContext(p.Ctx, heldQuery)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { held.Close() })
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "LOCK TABLE snapshot_relation_rows IN ACCESS EXCLUSIVE MODE", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO snapshot_relation_rows VALUES(2)", 1)
					id := identityID(t, p, a)
					snapshotBump(t, p, bumper, id)
					// No parameters on the direct route: pq must use simple Query,
					// not an implicit Parse/Bind that changes the acquisition boundary.
					query := "SELECT id,pg_current_snapshot()::text AS s,pg_visible_in_snapshot(" + snapshotQuote(id) + "::xid8,pg_current_snapshot()) AS visible FROM snapshot_relation_rows ORDER BY id"
					ctx, cancel := context.WithTimeout(p.Ctx, 8*time.Second)
					done := make(chan snapshotRemedyRows, 1)
					joined := false
					t.Cleanup(func() {
						cancel()
						if !joined {
							select {
							case <-done:
							case <-time.After(snapshotCleanupTimeout):
								t.Error("relation snapshot worker failed to join")
							}
						}
					})
					go func() {
						if held != nil {
							done <- snapshotRemedyRead(held.QueryContext(ctx, id))
						} else {
							done <- snapshotRemedyRead(reader.QueryContext(ctx, query))
						}
					}()
					poll, stop := context.WithTimeout(p.Ctx, 5*time.Second)
					defer stop()
					for {
						select {
						case early := <-done:
							joined = true
							t.Fatalf("query finished before pending relation admission: rows=%v error=%v", early.values, early.err)
						default:
						}
						var pending bool
						err := observer.QueryRowContext(poll, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='relation' AND relation::bigint=$1::bigint AND mode='AccessShareLock' AND NOT granted)", oid).Scan(&pending)
						if err != nil {
							t.Fatal("observe pending AccessShareLock", err)
						}
						if pending {
							break
						}
						runtime.Gosched()
					}
					fixture.LockExec(t, p.Ctx, a, completion, 0)
					var out snapshotRemedyRows
					select {
					case out = <-done:
						joined = true
					case <-ctx.Done():
						t.Fatal("relation snapshot worker", ctx.Err())
					}
					cancel()
					ids := [][]any{{int64(1)}}
					if completion == "COMMIT" {
						ids = append(ids, []any{int64(2)})
					}
					snapshotRemedyShape(t, out, []string{"id", "s", "visible"}, []string{"INT4", "TEXT", "BOOL"}, len(ids))
					s, ok := out.values[0][1].(string)
					if !ok {
						t.Fatalf("snapshot text %T", out.values[0][1])
					}
					for i, row := range out.values {
						want := []any{ids[i][0], s, true}
						if !reflect.DeepEqual(row, want) {
							t.Fatalf("admitted row %d = %v want %v", i, row, want)
						}
					}
					snapshotRemedyPeer(t, p, b, reader, s, id, true)
					snapshotBoth(t, p, b, reader, fixture.Select("SELECT id FROM snapshot_relation_rows ORDER BY id", []string{"id"}, []string{"INT4"}, ids))
				})
			})
		}
	}
}
