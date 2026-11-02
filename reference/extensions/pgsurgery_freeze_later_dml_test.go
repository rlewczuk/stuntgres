package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// These leaves distinguish a repaired header from perpetual row immunity.
// Expected effects are specified independently before the release oracle runs.
func freezeLaterDMLCases(t *testing.T) {
	for _, ending := range []string{"delete_commit", "delete_rollback", "update_commit"} {
		t.Run("later_"+ending, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'repair') RETURNING k,v,ctid,xmin,xmax")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=1", 1))
				freezeApply(t, p, b, c, a.tid)
				a = freezeRepaired(a)
				freezeExpect(t, p, b, c, a, z)
				freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				if ending == "update_commit" {
					next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='successor' WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
					if next.tid == a.tid || next.xmin == "2" || next.xmax != "0" || next.k != 1 || next.v != "successor" {
						t.Fatal("later UPDATE physical successor", next)
					}
					freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
					freezeExpect(t, p, b, c, next, z)
				} else {
					deleted := freezeOne(t, p, c, "DELETE FROM fs_part.heap WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
					if deleted.tid != a.tid || deleted.xmin != "2" || deleted.xmax == "0" || deleted.k != 1 || deleted.v != "repair" {
						t.Fatal("later DELETE physical header", deleted)
					}
					if ending == "delete_commit" {
						freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
						freezeExpect(t, p, b, c, z)
					} else {
						freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
						// Abort removes effective deletion, not its raw header xid.
						a.xmax = deleted.xmax
						freezeExpect(t, p, b, c, a, z)
					}
					freezeApply(t, p, b, c, a.tid)
					freezeExpect(t, p, b, c, freezeRepaired(a), z)
				}
			})
		})
	}
}

// A repaired private intermediate does not erase its owner's ordinary ancestor
// update, and a genuine later UPDATE still ends the repaired intermediate.
func freezePrivateRepairedChainCases(t *testing.T) {
	for _, commit := range []bool{false, true} {
		name := "private_repaired_chain_rollback"
		if commit {
			name = "private_repaired_chain_commit"
		}
		t.Run(name, func(t *testing.T) {
			freezeCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := freezeHorizon(t, p, b)
				defer horizon.Close()
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				a := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(1,'ancestor') RETURNING k,v,ctid,xmin,xmax")
				z := freezeOne(t, p, c, "INSERT INTO fs_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
				reader := freezeConn(t, p, b)
				defer reader.Close()
				freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				middle := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='middle' WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", a.tid)
				if middle.tid == a.tid || middle.xmin == "2" || middle.xmax != "0" {
					t.Fatal("ordinary private intermediate", middle)
				}
				freezeApply(t, p, b, c, middle.tid)
				freezeExpect(t, p, b, c, freezeRepaired(middle), z)
				next := freezeOne(t, p, c, "UPDATE fs_part.heap SET v='successor' WHERE ctid=$1::tid RETURNING k,v,ctid,xmin,xmax", middle.tid)
				if next.tid == a.tid || next.tid == middle.tid || next.xmin != middle.xmin || next.xmax != "0" || next.v != "successor" {
					t.Fatal("actual private successor", next)
				}
				freezeExpect(t, p, b, c, next, z)
				a.xmax = middle.xmin
				repaired := freezeRepaired(middle)
				repaired.xmax = middle.xmin
				freezeExpect(t, p, b, reader, a, repaired, z)
				if commit {
					freezeRun(t, p, b, c, fixture.Exec("COMMIT", 0))
					freezeExpect(t, p, b, reader, next, z)
				} else {
					freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
					freezeExpect(t, p, b, reader, a, repaired, z)
				}
			})
		})
	}
}
