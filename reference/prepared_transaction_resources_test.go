package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferencePreparedTransactionHeldResources(t *testing.T) {
	for _, kind := range []string{"relation", "row", "unique"} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(kind+"/"+completion, func(t *testing.T) {
				preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, waiter := referenceLockConnections(t, p.ctx, b.db)
					finish := sequenceCoreConn(t, p.ctx, b.db)
					referenceLockExec(t, p.ctx, finish, "CREATE TABLE prepared_hold(v INT UNIQUE)", 0)
					referenceLockExec(t, p.ctx, finish, "INSERT INTO prepared_hold VALUES(1)", 1)
					pid := 0
					if b.pg {
						pid = referenceLockPID(t, p.ctx, waiter)
					}
					referenceLockExec(t, p.ctx, a, "BEGIN", 0)
					switch kind {
					case "relation":
						referenceLockExec(t, p.ctx, a, "LOCK TABLE prepared_hold IN ACCESS EXCLUSIVE MODE", 0)
					case "row":
						referenceLockExec(t, p.ctx, a, "UPDATE prepared_hold SET v=2 WHERE v=1", 1)
					case "unique":
						referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_hold VALUES(3)", 1)
					}
					preparedReferencePrepare(t, p, b, a, "held_resource")
					b.db.SetMaxIdleConns(0)
					a.Close()
					query := "SELECT v FROM prepared_hold ORDER BY v"
					if kind == "row" {
						query = "UPDATE prepared_hold SET v=v+10 WHERE v=1"
					}
					if kind == "unique" {
						query = "INSERT INTO prepared_hold VALUES(3)"
					}
					referenceLockExec(t, p.ctx, waiter, "SET lock_timeout='10ms'", 0)
					_, err := waiter.ExecContext(p.ctx, query)
					want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
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
					if !b.pg {
						want.Context = ""
					}
					referenceAssertDiagnostic(t, err, &want)
					preparedReferenceOne(t, p, finish, "held_resource")
					referenceLockExec(t, p.ctx, waiter, "SET lock_timeout=0", 0)
					cancelCtx, cancel := context.WithCancel(p.ctx)
					t.Cleanup(cancel)
					canceled := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						if b.pg {
							return referenceRowExecute(ctx, waiter, query, false)
						}
						return referenceRowExecute(cancelCtx, waiter, query, false)
					})
					if b.pg {
						referenceRowWait(t, p.ctx, pid, 0)
						var ok bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&ok); err != nil || !ok {
							t.Fatal(ok, err)
						}
					} else {
						cancel()
					}
					canceledOutcome := canceled()
					if b.pg {
						cancelWant := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
						switch kind {
						case "row":
							cancelWant.Context = `while updating tuple (0,1) in relation "prepared_hold"`
						case "unique":
							cancelWant.Context = `while inserting index tuple (0,4) in relation "prepared_hold_v_key"`
						}
						referenceAssertDiagnostic(t, canceledOutcome.err, &cancelWant)
					} else if !errors.Is(canceledOutcome.err, context.Canceled) {
						t.Fatal(canceledOutcome.err)
					}
					preparedReferenceOne(t, p, finish, "held_resource")
					await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
						if kind == "relation" {
							return referenceRowQuery(ctx, waiter, query, true)
						}
						return referenceRowExecute(ctx, waiter, query, true)
					})
					// Prepared transactions appear as pid 0 in pg_blocking_pids; origin
					// transport closure is not a resource-release barrier.
					if b.pg {
						referenceRowWait(t, p.ctx, pid, 0)
					}
					referenceLockExec(t, p.ctx, finish, completion+" PREPARED 'held_resource'", 0)
					o := await()
					if kind == "relation" {
						referenceRowValues(t, o, 1)
					} else if kind == "unique" && completion == "COMMIT" {
						duplicateWant := uniqueReferenceDuplicate("prepared_hold", "prepared_hold_v_key", "Key (v)=(3) already exists.")
						referenceAssertDiagnostic(t, o.err, &duplicateWant)
					} else {
						if o.err != nil {
							t.Fatal(o.err)
						}
						count := int64(1)
						if kind == "row" && completion == "COMMIT" {
							count = 0
						}
						referenceLockAffected(t, o.result, count)
					}
					switch kind {
					case "relation":
						preparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", 1)
					case "row":
						v := int64(2)
						if completion == "ROLLBACK" {
							v = 11
						}
						preparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", v)
					case "unique":
						preparedReferenceRows(t, p, b, finish, "SELECT v FROM prepared_hold ORDER BY v", 1, 3)
					}
					preparedReferenceAbsent(t, p, finish, "held_resource")
				})
			})
		}
	}
}

func TestReferencePreparedTransactionStagedCatalogSequence(t *testing.T) {
	for _, completion := range []string{"COMMIT", "ROLLBACK"} {
		t.Run(completion, func(t *testing.T) {
			preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, peer := referenceLockConnections(t, p.ctx, b.db)
				referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_catalog(v INT,discard INT)", 0)
				referenceLockExec(t, p.ctx, peer, "INSERT INTO prepared_catalog(v,discard) VALUES(1,11)", 1)
				referenceLockExec(t, p.ctx, peer, "CREATE INDEX prepared_index ON prepared_catalog(v)", 0)
				referenceLockExec(t, p.ctx, peer, "CREATE SEQUENCE prepared_restart START 10", 0)
				referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_new(v INT)", 0)
				referenceLockExec(t, p.ctx, a, "BEGIN", 0)
				referenceLockExec(t, p.ctx, a, "DELETE FROM prepared_catalog WHERE v=1", 1)
				referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_catalog(v,discard) VALUES(7,17)", 1)
				referenceLockExec(t, p.ctx, a, "ALTER TABLE prepared_catalog ALTER v SET NOT NULL", 0)
				referenceLockExec(t, p.ctx, a, "ALTER TABLE prepared_catalog DROP COLUMN discard", 0)
				referenceLockExec(t, p.ctx, a, "CREATE SEQUENCE prepared_created START 60", 0)
				referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_new VALUES(9)", 1)
				referenceLockExec(t, p.ctx, a, "ALTER SEQUENCE prepared_restart RESTART WITH 40", 0)
				preparedReferencePrepare(t, p, b, a, "catalog")
				// Catalog and generator holds prevent peer access until completion; query
				// only committed pg_class metadata, not a blocking relation descriptor.
				var n int
				if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_created'").Scan(&n); err != nil || n != 0 {
					t.Fatal(n, err)
				}
				preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new")
				if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
					t.Fatal(n, err)
				}
				referenceLockExec(t, p.ctx, peer, completion+" PREPARED 'catalog'", 0)
				if completion == "COMMIT" {
					preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog", 7)
					preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new", 9)
					sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('prepared_restart')", 40)
					sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('prepared_created')", 60)
					if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog WHERE v=7", 7)
					rows, err := peer.QueryContext(p.ctx, "SELECT * FROM prepared_catalog")
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					rows.Close()
					if err != nil || len(columns) != 1 || columns[0] != "v" {
						t.Fatal(columns, err)
					}
					if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_constraint WHERE conname='prepared_catalog_v_not_null' AND contype='n'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
				} else {
					preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_catalog", 1)
					sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('prepared_restart')", 10)
					if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_index'").Scan(&n); err != nil || n != 1 {
						t.Fatal(n, err)
					}
					if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_class WHERE relname='prepared_created'").Scan(&n); err != nil || n != 0 {
						t.Fatal(n, err)
					}
					preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_new")
					referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT discard FROM prepared_catalog WHERE v=1", []string{"discard"}, []string{"INT4"}, [][]any{{int64(11)}}))
					if err := peer.QueryRowContext(p.ctx, "SELECT count(*) FROM pg_constraint WHERE conname='prepared_catalog_v_not_null' AND contype='n'").Scan(&n); err != nil || n != 0 {
						t.Fatal(n, err)
					}
				}
			})
		})
	}
}
