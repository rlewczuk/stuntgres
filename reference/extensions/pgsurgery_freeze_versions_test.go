package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryFreezeValues(t *testing.T) {
	freezeVacuumRepairCase(t)
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		freezeRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1,'kept'),(2,'direct'),(3,'held')", 3))
		h := freezeCapture(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap ORDER BY k")
		if len(h) != 3 {
			t.Fatal("inserted versions", h)
		}
		if h[0].tid == h[1].tid || h[1].tid == h[2].tid || h[0].tid == h[2].tid {
			t.Fatal("distinct inserts share TID")
		}
		freezeApply(t, p, b, c, h[1].tid)
		h[1] = freezeRepaired(h[1])
		freezeExpect(t, p, b, c, h...)
		st, err := c.PrepareContext(p.Ctx, freezeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: freezeSQL}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs(h[2].tid)))
		}
		h[2] = freezeRepaired(h[2])
		freezeExpect(t, p, b, c, h...)
	})
}

func TestReferencePgsurgeryFreezeVersionIdentity(t *testing.T) {
	freezeSiblingDMLCases(t)
	freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		horizon := freezeHorizon(t, p, b)
		defer horizon.Close()
		defer horizon.ExecContext(p.Ctx, "ROLLBACK")
		a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
		z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
		d := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
		if a.tid == d.tid {
			t.Fatal("UPDATE did not create a new physical version")
		}
		freezeApply(t, p, b, c, a.tid)
		freezeExpect(t, p, b, c, freezeRepaired(a), d, z)
		freezeApply(t, p, b, c, d.tid)
		freezeExpect(t, p, b, c, freezeRepaired(a), freezeRepaired(d), z)
	})
}

func TestReferencePgsurgeryFreezeRetainedVersions(t *testing.T) {
	for _, disposition := range []string{"deleted", "rollback_insert", "rollback_update", "savepoint", "intermediate", "statement_error", "session_end", "prepared_abort"} {
		t.Run(disposition, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				var original, target freezeHeader
				if disposition == "deleted" || disposition == "rollback_update" || disposition == "intermediate" {
					original = freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'A') RETURNING k,v,ctid,xmin,xmax")
				}
				writer := c
				if disposition == "session_end" {
					writer = freezeConn(t, p, b)
					defer writer.Close()
				}
				run := func(q string) { freezeRun(t, p, b, writer, fixture.Exec(q, 0)) }
				if disposition == "deleted" {
					target = original
					freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=1", 1))
				} else {
					run("BEGIN")
					defer writer.ExecContext(p.Ctx, "ROLLBACK")
					if disposition == "savepoint" {
						run("SAVEPOINT fs_before")
					}
					q := "INSERT INTO fs_part.heap VALUES(1,'B') RETURNING k,v,ctid,xmin,xmax"
					if disposition == "rollback_update" || disposition == "intermediate" {
						q = "UPDATE fs_part.heap SET v='B' WHERE k=1 RETURNING k,v,ctid,xmin,xmax"
					}
					target = freezeOne(t, p, writer, q)
					switch disposition {
					case "savepoint":
						run("ROLLBACK TO fs_before")
						run("COMMIT")
					case "intermediate":
						later := freezeOne(t, p, writer, "UPDATE fs_part.heap SET v='C' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
						if later.tid == target.tid {
							t.Fatal("private successor reused TID")
						}
						run("ROLLBACK")
					case "statement_error":
						freezeRun(t, p, b, writer, freezeError("SELECT 1/0", "22012", "division by zero"))
						run("ROLLBACK")
					case "session_end":
						// Retire the physical borrower as well as its pool lease.
						var closeErr error
						err := writer.Raw(func(raw any) error {
							closeErr = raw.(driver.Conn).Close()
							return driver.ErrBadConn
						})
						if closeErr != nil || !errors.Is(err, driver.ErrBadConn) {
							t.Fatal("ending physical owner", closeErr, err)
						}
					case "prepared_abort":
						run("PREPARE TRANSACTION 'fs_retained_abort'")
						defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED 'fs_retained_abort'")
						run("ROLLBACK PREPARED 'fs_retained_abort'")
					default:
						run("ROLLBACK")
					}
				}
				// No scan or maintenance between discard and repair: the captured
				// slot must remain genuine, not reconstructed from visible data.
				freezeApply(t, p, b, c, target.tid)
				if disposition == "rollback_update" || disposition == "intermediate" {
					// Abort invalidates deletion logically, but does not erase the
					// predecessor's physical xmax transaction identifier.
					original.xmax = target.xmin
					freezeExpect(t, p, b, c, original, freezeRepaired(target), z)
				} else {
					freezeExpect(t, p, b, c, freezeRepaired(target), z)
				}
			})
		})
	}
}

func TestReferencePgsurgeryFreezeTransactions(t *testing.T) {
	freezeLaterDMLCases(t)
	freezePrivateRepairedChainCases(t)
	for _, ending := range []string{"rollback", "savepoint", "later_error", "read_only", "writer_commit", "writer_abort", "private_delete_commit", "private_delete_abort"} {
		t.Run(ending, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				writer := freezeConn(t, p, b)
				defer writer.Close()
				defer writer.ExecContext(p.Ctx, "ROLLBACK")
				wr := func(q string, count int64) { freezeRun(t, p, b, writer, fixture.Exec(q, count)) }
				var target freezeHeader
				liveWriter := ending == "writer_commit" || ending == "writer_abort" || ending == "private_delete_commit" || ending == "private_delete_abort"
				if liveWriter {
					if ending == "private_delete_commit" || ending == "private_delete_abort" {
						target = freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'repair') RETURNING k,v,ctid,xmin,xmax")
					}
					wr("BEGIN", 0)
					if ending == "writer_commit" || ending == "writer_abort" {
						target = freezeOne(t, p, writer, "INSERT INTO fs_part.heap VALUES(1,'repair') RETURNING k,v,ctid,xmin,xmax")
					} else {
						wr("DELETE FROM fs_part.heap WHERE k=1", 1)
					}
					wr("INSERT INTO fs_part.heap VALUES(3,'private')", 1)
					freezeApply(t, p, b, c, target.tid)
					freezeExpect(t, p, b, c, freezeRepaired(target), z)
					if ending == "writer_commit" || ending == "private_delete_commit" {
						wr("COMMIT", 0)
						private := freezeOne(t, p, c, "SELECT k,v,ctid,xmin,xmax FROM fs_part.heap WHERE k=3")
						freezeExpect(t, p, b, c, freezeRepaired(target), z, private)
					} else {
						wr("ROLLBACK", 0)
						freezeExpect(t, p, b, c, freezeRepaired(target), z)
					}
					return
				}
				target = freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'repair') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=1", 1))
				begin := "BEGIN"
				if ending == "read_only" {
					begin = "BEGIN READ ONLY"
				}
				freezeRun(t, p, b, c, fixture.Exec(begin, 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				if ending == "savepoint" {
					freezeRun(t, p, b, c, fixture.Exec("SAVEPOINT fs_before", 0))
				}
				freezeApply(t, p, b, c, target.tid)
				if ending == "later_error" {
					freezeRun(t, p, b, c, freezeError("SELECT 1/0", "22012", "division by zero"))
				}
				if ending == "savepoint" {
					freezeRun(t, p, b, c, fixture.Exec("ROLLBACK TO fs_before", 0))
				}
				freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				freezeExpect(t, p, b, writer, freezeRepaired(target), z)
			})
		})
	}
}
