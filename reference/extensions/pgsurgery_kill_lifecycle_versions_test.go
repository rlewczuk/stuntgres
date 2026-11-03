package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func ksLifeApply(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tids ...string) {
	t.Helper()
	freezeRun(t, p, b, c, fixture.Select(killSQL, []string{"killed"}, []string{"VOID"}, [][]any{{""}}, "ks_part.heap", freezeTIDs(tids...)))
}

func ksLifeRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, values ...[]any) {
	t.Helper()
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v FROM ks_part.heap ORDER BY k,v", []string{"k", "v"}, []string{"INT4", "TEXT"}, values))
}

func ksLifeHorizon(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := freezeConn(t, p, b)
	// Register before BEGIN: failed assertions must not leave the snapshot owner.
	t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK"); c.Close() })
	freezeRun(t, p, b, c, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM ks_part.heap", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	return c
}

func TestReferencePgsurgeryKillVersions(t *testing.T) {
	t.Run("retained_predecessor_and_current_successor", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			horizon := ksLifeHorizon(t, p, b)
			defer horizon.ExecContext(p.Ctx, "ROLLBACK")
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
			z := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(2,'sentinel') RETURNING k,v,ctid,xmin,xmax")
			next := freezeOne(t, p, c, "UPDATE ks_part.heap SET v='successor' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
			if a.tid == next.tid || a.k != next.k || next.v != "successor" {
				t.Fatal("distinct same-row successor", a, next)
			}
			ksLifeApply(t, p, b, c, a.tid)
			ksRows(t, p, b, c, next, z)
			ksLifeApply(t, p, b, c, next.tid)
			ksRows(t, p, b, c, z)
		})
	})
	// heap_force_common does not inspect xmin/xmax or visibility. A killed
	// private head is absent even to its owner; abort cannot resurrect that exact
	// slot. A distinct successor is not killed by its predecessor's RowID.
	// These source-derived writer expectations require the root's PG oracle.
	for _, owner := range []string{"caller", "peer"} {
		for _, mutation := range []string{"insert", "update_head", "update_predecessor", "intermediate", "delete"} {
			for _, ending := range []string{"COMMIT", "ROLLBACK"} {
				t.Run(owner+"/"+mutation+"/"+ending, func(t *testing.T) {
					ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						horizon := ksLifeHorizon(t, p, b)
						defer horizon.ExecContext(p.Ctx, "ROLLBACK")
						freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'sentinel')", 1))
						var old freezeHeader
						if mutation != "insert" {
							old = freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
						}
						writer := freezeConn(t, p, b)
						defer writer.Close()
						defer writer.ExecContext(p.Ctx, "ROLLBACK")
						freezeRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
						var target freezeHeader
						switch mutation {
						case "insert":
							target = freezeOne(t, p, writer, "INSERT INTO ks_part.heap VALUES(1,'private') RETURNING k,v,ctid,xmin,xmax")
						case "delete":
							target = old
							freezeRun(t, p, b, writer, fixture.Exec("DELETE FROM ks_part.heap WHERE k=1", 1))
						default:
							target = freezeOne(t, p, writer, "UPDATE ks_part.heap SET v='private' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
							if target.tid == old.tid {
								t.Fatal("private UPDATE reused predecessor")
							}
							if mutation == "update_predecessor" {
								target = old
							}
							if mutation == "intermediate" {
								last := freezeOne(t, p, writer, "UPDATE ks_part.heap SET v='successor' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
								if last.tid == target.tid || last.tid == old.tid {
									t.Fatal("private lineage reused identity")
								}
							}
						}
						freezeRun(t, p, b, writer, fixture.Exec("INSERT INTO ks_part.heap VALUES(3,'writer')", 1))
						killer := c
						if owner == "caller" {
							killer = writer
						}
						ksLifeApply(t, p, b, killer, target.tid)
						own := [][]any{{int64(2), "sentinel"}, {int64(3), "writer"}}
						if mutation == "update_predecessor" {
							own = append([][]any{{int64(1), "private"}}, own...)
						}
						if mutation == "intermediate" {
							own = append([][]any{{int64(1), "successor"}}, own...)
						}
						ksLifeRows(t, p, b, writer, own...)
						peer := [][]any{{int64(2), "sentinel"}}
						if mutation == "update_head" || mutation == "intermediate" {
							peer = append([][]any{{int64(1), "old"}}, peer...)
						}
						ksLifeRows(t, p, b, c, peer...)
						freezeRun(t, p, b, writer, fixture.Exec(ending, 0))
						want := [][]any{{int64(2), "sentinel"}}
						if ending == "COMMIT" {
							want = append(want, []any{int64(3), "writer"})
							if mutation == "update_predecessor" {
								want = append([][]any{{int64(1), "private"}}, want...)
							}
							if mutation == "intermediate" {
								want = append([][]any{{int64(1), "successor"}}, want...)
							}
						} else if mutation == "update_head" || mutation == "intermediate" {
							want = append([][]any{{int64(1), "old"}}, want...)
						}
						ksLifeRows(t, p, b, c, want...)
					})
				})
			}
		}
	}
	for _, retained := range []string{"deleted", "aborted_insert", "aborted_update", "savepoint_discard"} {
		t.Run(retained, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				horizon := ksLifeHorizon(t, p, b)
				defer horizon.ExecContext(p.Ctx, "ROLLBACK")
				var target freezeHeader
				if retained == "deleted" || retained == "aborted_update" {
					target = freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
				}
				freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'sentinel')", 1))
				if retained == "deleted" {
					freezeRun(t, p, b, c, fixture.Exec("DELETE FROM ks_part.heap WHERE k=1", 1))
				} else {
					freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					if retained == "savepoint_discard" {
						freezeRun(t, p, b, c, fixture.Exec("SAVEPOINT before_insert", 0))
					}
					q := "INSERT INTO ks_part.heap VALUES(1,'discarded') RETURNING k,v,ctid,xmin,xmax"
					if retained == "aborted_update" {
						q = "UPDATE ks_part.heap SET v='discarded' WHERE k=1 RETURNING k,v,ctid,xmin,xmax"
					}
					target = freezeOne(t, p, c, q)
					if retained == "savepoint_discard" {
						freezeRun(t, p, b, c, fixture.Exec("ROLLBACK TO before_insert", 0))
					}
					freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				}
				// No intervening scan/pruning before targeting the genuine slot.
				ksLifeApply(t, p, b, c, target.tid)
				want := [][]any{{int64(2), "sentinel"}}
				if retained == "aborted_update" {
					want = append([][]any{{int64(1), "old"}}, want...)
				}
				ksLifeRows(t, p, b, c, want...)
			})
		})
	}
	for _, transport := range []string{"shape", "vacuum", "truncate", "drop_recreate"} {
		t.Run("maintenance_"+transport, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'killed') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
				ksLifeApply(t, p, b, c, a.tid)
				switch transport {
				case "shape":
					for _, q := range []string{"ALTER TABLE ks_part.heap ADD COLUMN extra integer DEFAULT 7", "ALTER TABLE ks_part.heap DROP COLUMN extra", "ALTER TABLE ks_part.heap ALTER COLUMN v TYPE varchar(40)", "ALTER TABLE ks_part.heap ALTER COLUMN v TYPE text"} {
						freezeRun(t, p, b, c, fixture.Exec(q, 0))
					}
				case "vacuum":
					freezeRun(t, p, b, c, fixture.Exec("VACUUM ks_part.heap", 0))
					freezeRun(t, p, b, c, fixture.Exec("CREATE INDEX rebuilt ON ks_part.heap(k)", 0))
					freezeRun(t, p, b, c, fixture.Exec("UPDATE ks_part.heap SET v='survivor' WHERE k=2", 1))
					freezeRun(t, p, b, c, fixture.Exec("DELETE FROM ks_part.heap WHERE k=1", 0))
				case "truncate", "drop_recreate":
					if transport == "truncate" {
						freezeRun(t, p, b, c, fixture.Exec("TRUNCATE ks_part.heap", 0))
					} else {
						freezeRun(t, p, b, c, fixture.Exec("DROP TABLE ks_part.heap", 0))
						freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE ks_part.heap(k integer,v text)", 0))
					}
					// Public coordinates can be reused by PG. New incarnation rows
					// must not inherit death; do not invoke a stale bare coordinate.
					freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
				}
				ksLifeRows(t, p, b, c, []any{int64(2), "survivor"})
			})
		})
	}
	t.Run("freeze_after_kill_skips_dead", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'killed') RETURNING k,v,ctid,xmin,xmax")
			freezeRun(t, p, b, c, freezeVoid("SELECT ks_part.heap_force_freeze($1::regclass,$2::tid[]) AS repaired", "ks_part.heap", freezeTIDs(a.tid)))
			ksLifeApply(t, p, b, c, a.tid)
			notices = nil
			freezeRun(t, p, b, c, freezeVoid("SELECT ks_part.heap_force_freeze($1::regclass,$2::tid[]) AS repaired", "ks_part.heap", freezeTIDs(a.tid)))
			ksLifeDeadNotice(t, notices, a.tid)
			ksLifeRows(t, p, b, c)
		})
	})
}
