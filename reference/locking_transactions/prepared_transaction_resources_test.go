package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePreparedTransactionHeldResources(t *testing.T) {
	for _, kind := range []string{"relation", "row", "unique"} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(kind+"/"+completion, func(t *testing.T) {
				fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, waiter := fixture.LockConnections(t, p.Ctx, b.DB)
					finish := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
					fixture.LockExec(t, p.Ctx, finish, "CREATE TABLE prepared_hold(v INT UNIQUE)", 0)
					fixture.LockExec(t, p.Ctx, finish, "INSERT INTO prepared_hold VALUES(1)", 1)
					pid := 0
					if b.PG {
						pid = fixture.LockPID(t, p.Ctx, waiter)
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					switch kind {
					case "relation":
						fixture.LockExec(t, p.Ctx, a, "LOCK TABLE prepared_hold IN ACCESS EXCLUSIVE MODE", 0)
					case "row":
						fixture.LockExec(t, p.Ctx, a, "UPDATE prepared_hold SET v=2 WHERE v=1", 1)
					case "unique":
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_hold VALUES(3)", 1)
					}
					fixture.PreparedReferencePrepare(t, p, b, a, "held_resource")
					b.DB.SetMaxIdleConns(0)
					a.Close()
					query := "SELECT v FROM prepared_hold ORDER BY v"
					if kind == "row" {
						query = "UPDATE prepared_hold SET v=v+10 WHERE v=1"
					}
					if kind == "unique" {
						query = "INSERT INTO prepared_hold VALUES(3)"
					}
					fixture.LockExec(t, p.Ctx, waiter, "SET lock_timeout='10ms'", 0)
					_, err := waiter.ExecContext(p.Ctx, query)
					want := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
					switch kind {
					case "relation":
						want.Position = "15"
					case "row":
						want.Context = `while updating tuple (0,1) in relation "prepared_hold"`
					case "unique":
						want.Context = `while inserting index tuple (0,3) in relation "prepared_hold_v_key"`
					}
					// Native has no PostgreSQL physical tuple coordinates. Preserve
					// the independently observed server Context only on PostgreSQL.
					if !b.PG {
						want.Context = ""
					}
					fixture.AssertDiagnostic(t, err, &want)
					fixture.PreparedReferenceOne(t, p, finish, "held_resource")
					fixture.LockExec(t, p.Ctx, waiter, "SET lock_timeout=0", 0)
					cancelCtx, cancel := context.WithCancel(p.Ctx)
					t.Cleanup(cancel)
					canceled := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						if b.PG {
							return fixture.RowExecute(ctx, waiter, query, false)
						}
						return fixture.RowExecute(cancelCtx, waiter, query, false)
					})
					if b.PG {
						fixture.RowWait(t, p.Ctx, pid, 0)
						var ok bool
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
							t.Fatal(ok, err)
						}
					} else {
						cancel()
					}
					canceledOutcome := canceled()
					if b.PG {
						cancelWant := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
						switch kind {
						case "row":
							cancelWant.Context = `while updating tuple (0,1) in relation "prepared_hold"`
						case "unique":
							cancelWant.Context = `while inserting index tuple (0,4) in relation "prepared_hold_v_key"`
						}
						fixture.AssertDiagnostic(t, canceledOutcome.Err, &cancelWant)
					} else if !errors.Is(canceledOutcome.Err, context.Canceled) {
						t.Fatal(canceledOutcome.Err)
					}
					fixture.PreparedReferenceOne(t, p, finish, "held_resource")
					await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						if kind == "relation" {
							return fixture.RowQuery(ctx, waiter, query, true)
						}
						return fixture.RowExecute(ctx, waiter, query, true)
					})
					// Prepared transactions appear as pid 0 in pg_blocking_pids; origin
					// transport closure is not a resource-release barrier.
					if b.PG {
						fixture.RowWait(t, p.Ctx, pid, 0)
					}
					fixture.LockExec(t, p.Ctx, finish, completion+" PREPARED 'held_resource'", 0)
					o := await()
					if kind == "relation" {
						fixture.RowValues(t, o, 1)
					} else if kind == "unique" && completion == "COMMIT" {
						duplicateWant := fixture.UniqueReferenceDuplicate("prepared_hold", "prepared_hold_v_key", "Key (v)=(3) already exists.")
						fixture.AssertDiagnostic(t, o.Err, &duplicateWant)
					} else {
						if o.Err != nil {
							t.Fatal(o.Err)
						}
						count := int64(1)
						if kind == "row" && completion == "COMMIT" {
							count = 0
						}
						fixture.LockAffected(t, o.Result, count)
					}
					switch kind {
					case "relation":
						fixture.PreparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", 1)
					case "row":
						v := int64(2)
						if completion == "ROLLBACK" {
							v = 11
						}
						fixture.PreparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", v)
					case "unique":
						fixture.PreparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", 1, 3)
					}
					fixture.PreparedReferenceAbsent(t, p, finish, "held_resource")
				})
			})
		}
	}
}

func TestReferencePreparedTransactionStagedCatalogSequence(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
				fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_catalog(v INT,discard INT)", 0)
				fixture.LockExec(t, p.Ctx, peer, "INSERT INTO prepared_catalog(v,discard) VALUES(1,11)", 1)
				fixture.LockExec(t, p.Ctx, peer, "CREATE INDEX prepared_index ON prepared_catalog(v)", 0)
				fixture.LockExec(t, p.Ctx, peer, "CREATE SEQUENCE prepared_restart START 10", 0)
				fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_new(v INT)", 0)
				fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
				fixture.LockExec(t, p.Ctx, a, "DELETE FROM prepared_catalog WHERE v=1", 1)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_catalog(v,discard) VALUES(7,17)", 1)
				fixture.LockExec(t, p.Ctx, a, "ALTER TABLE prepared_catalog ALTER v SET NOT NULL", 0)
				fixture.LockExec(t, p.Ctx, a, "ALTER TABLE prepared_catalog DROP COLUMN discard", 0)
				fixture.LockExec(t, p.Ctx, a, "CREATE SEQUENCE prepared_created START 60", 0)
				fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_new VALUES(9)", 1)
				fixture.LockExec(t, p.Ctx, a, "ALTER SEQUENCE prepared_restart RESTART WITH 40", 0)
				fixture.PreparedReferencePrepare(t, p, b, a, "catalog")
				// Catalog and generator holds prevent peer access until completion; query
				// only committed pg_class metadata, not a blocking relation descriptor.
				var n int
				if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_created'").Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new")
				if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				fixture.LockExec(t, p.Ctx, peer, completion+" PREPARED 'catalog'", 0)
				if completion == "COMMIT" {
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog", 7)
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new", 9)
					fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('prepared_restart')", 40)
					fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('prepared_created')", 60)
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog WHERE v=7", 7)
					rows, err := peer.QueryContext(p.Ctx, "SELECT * FROM prepared_catalog")
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					rows.Close()
					if err != nil || len(columns) != 1 || columns[0] != "v" {
						t.Fatal(columns, err)
					}
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_constraint WHERE conname='prepared_catalog_v_not_null' AND contype='n'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				} else {
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog", 1)
					fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('prepared_restart')", 10)
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_created'").Scan(&n); err != nil || n != 0 {
						t.Fatal(n, err)
					}
					fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new")
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT discard FROM prepared_catalog WHERE v=1", []string{"discard"}, []string{"INT4"}, [][]any{{int64(11)}}))
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_constraint WHERE conname='prepared_catalog_v_not_null' AND contype='n'").Scan(&n); err != nil || n != 0 {
						t.Fatal(n, err)
					}
				}
			})
		})
	}
}
