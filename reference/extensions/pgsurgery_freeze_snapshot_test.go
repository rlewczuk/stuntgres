package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// REL_18_6 nodeModifyTable.c's UPDATE and DELETE TM_Deleted branches require
// this exact diagnostic under IsolationUsesXactSnapshot(). Surgery resets the
// aborted insertion header; it does not exempt a later committed deletion.
func TestReferencePgsurgeryFreezeAbortedSnapshotDelete(t *testing.T) {
	for _, isolation := range []struct {
		name string
		sql  string
	}{
		{"repeatable_read", "REPEATABLE READ"},
		{"serializable", "SERIALIZABLE"},
	} {
		t.Run(isolation.name, func(t *testing.T) {
			for _, action := range []string{"update", "delete"} {
				t.Run(action, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						route := "direct"
						if prepared {
							route = "prepared"
						}
						t.Run(route, func(t *testing.T) {
							freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								ctx, cancel := context.WithCancel(p.Ctx)
								defer cancel()
								run := func(conn *sql.Conn, step fixture.Step) {
									fixture.RunStep(t, ctx, conn, b.Recorder, step)
								}
								finish := func(conn *sql.Conn) {
									// End every owner before freezeCase's schema cleanup, even
									// after an assertion or scenario cancellation. No workers
									// or open Rows survive a phase of this serial schedule.
									cancel()
									cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
									defer done()
									if _, err := conn.ExecContext(cleanup, "ROLLBACK"); err != nil {
										t.Error("snapshot owner cleanup", err)
									}
									if err := conn.Close(); err != nil {
										t.Error(err)
									}
								}

								horizon := freezeHorizon(t, p, b)
								defer finish(horizon)
								z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
								run(c, fixture.Exec("BEGIN", 0))
								defer func() {
									cleanup, done := context.WithTimeout(context.Background(), 2*time.Second)
									defer done()
									if _, err := c.ExecContext(cleanup, "ROLLBACK"); err != nil {
										t.Error("insertion owner cleanup", err)
									}
								}()
								a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'aborted') RETURNING k,v,ctid,xmin,xmax")
								if a.k != 1 || a.v != "aborted" || a.xmin == "2" || a.xmax != "0" || a.tid == z.tid {
									t.Fatal("genuine private insertion", a, z)
								}
								run(c, fixture.Exec("ROLLBACK", 0))
								// Do not scan/prune the aborted slot before repairing its
								// captured identity. The same real tuple must reappear.
								freezeApply(t, p, b, c, a.tid)
								a = freezeRepaired(a)
								freezeExpect(t, p, b, c, a, z)

								reader := freezeConn(t, p, b)
								defer finish(reader)
								run(reader, fixture.Exec("BEGIN ISOLATION LEVEL "+isolation.sql, 0))
								// BEGIN alone is not a snapshot witness. Drain and close
								// these actual reads before the peer mutates the tuple.
								freezeExpect(t, p, b, reader, a, z)
								q := "UPDATE fs_part.heap SET v='unexpected' WHERE ctid=$1::tid"
								if action == "delete" {
									q = "DELETE FROM fs_part.heap WHERE ctid=$1::tid"
								}
								var held *sql.Stmt
								if prepared {
									var err error
									held, err = reader.PrepareContext(ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									defer held.Close()
								}

								peer := freezeConn(t, p, b)
								defer finish(peer)
								run(peer, fixture.Exec("BEGIN", 0))
								deleted := freezeOne(t, p, peer, "DELETE FROM fs_part.heap WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
								if deleted.k != a.k || deleted.v != a.v || deleted.tid != a.tid || deleted.xmin != "2" || deleted.xmax == "0" {
									t.Fatal("peer deleted wrong physical tuple or omitted its real end", deleted, a)
								}
								run(peer, fixture.Exec("COMMIT", 0))
								freezeExpect(t, p, b, c, z)
								// Stable visibility still includes A. Do not demand an old
								// raw xmax: committed deletion changes its physical header.
								run(reader, fixture.Select("SELECT k,v,ctid,xmin FROM fs_part.heap ORDER BY k,v",
									[]string{"k", "v", "ctid", "xmin"}, []string{"INT4", "TEXT", "TID", "XID"},
									[][]any{{a.k, a.v, a.tid, a.xmin}, {z.k, z.v, z.tid, z.xmin}}))

								want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent delete")
								step := fixture.ImplicitError(q, want, a.tid)
								if prepared {
									executor := fixture.HeldStatement{Executor: reader, Statement: held, SQL: q}
									fixture.RunStep(t, ctx, executor, b.Recorder, step)
								} else {
									run(reader, step)
								}
								run(reader, fixture.Exec("ROLLBACK", 0))
								freezeExpect(t, p, b, reader, z)
								freezeExpect(t, p, b, c, z)
							})
						})
					}
				})
			}
		})
	}
}
