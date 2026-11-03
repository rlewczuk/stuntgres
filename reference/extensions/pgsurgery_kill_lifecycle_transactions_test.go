package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillTransactions(t *testing.T) {
	for _, ending := range []string{"rollback", "savepoint", "later_statement_error", "later_expression_error", "read_only"} {
		t.Run(ending, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'killed') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
				if ending == "later_expression_error" {
					freezeRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE ks_part.failure_seq START 1", 0))
				}
				begin := "BEGIN"
				if ending == "read_only" {
					begin = "BEGIN READ ONLY"
				}
				freezeRun(t, p, b, c, fixture.Exec(begin, 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				if ending == "savepoint" {
					freezeRun(t, p, b, c, fixture.Exec("SAVEPOINT before_kill", 0))
				}
				if ending == "later_expression_error" {
					// Volatile nextval prevents planning-time constant folding of
					// division. Released target-list evaluation reaches kill first.
					q := "SELECT ks_part.heap_force_kill($1::regclass,$2::tid[]),1/(nextval('ks_part.failure_seq')::integer-1)"
					freezeRun(t, p, b, c, freezeError(q, "22012", "division by zero", "ks_part.heap", freezeTIDs(a.tid)))
				} else {
					// Read-only surgery is predicted to succeed: the released C
					// function opens RX and changes pages without PreventCommandIfReadOnly.
					// Root must reconcile this explicit assertion with pinned PG.
					ksLifeApply(t, p, b, c, a.tid)
				}
				if ending == "later_statement_error" {
					freezeRun(t, p, b, c, freezeError("SELECT 1/0", "22012", "division by zero"))
				}
				if ending == "savepoint" {
					freezeRun(t, p, b, c, fixture.Exec("ROLLBACK TO before_kill", 0))
				}
				freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				ksLifeRows(t, p, b, c, []any{int64(2), "survivor"})
			})
		})
	}
	t.Run("old_rr_later_scan_and_detached_rows", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
			freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
			reader := freezeConn(t, p, b)
			defer reader.Close()
			defer reader.ExecContext(p.Ctx, "ROLLBACK")
			freezeRun(t, p, b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
			ksLifeRows(t, p, b, reader, []any{int64(1), "old"}, []any{int64(2), "survivor"})
			// Native Rows are detached eagerly; pq owns streaming buffer pins.
			// Do not hold an unread PG tuple pin across surgery's cleanup lock.
			var old *sql.Rows
			if !b.PG {
				var err error
				old, err = reader.QueryContext(p.Ctx, "SELECT k,v FROM ks_part.heap ORDER BY k")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
			}
			next := freezeOne(t, p, c, "UPDATE ks_part.heap SET v='successor' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
			if next.tid == a.tid {
				t.Fatal("UPDATE did not create successor")
			}
			ksLifeRows(t, p, b, reader, []any{int64(1), "old"}, []any{int64(2), "survivor"})
			ksLifeApply(t, p, b, c, a.tid)
			ksLifeRows(t, p, b, reader, []any{int64(2), "survivor"})
			ksLifeRows(t, p, b, c, []any{int64(1), "successor"}, []any{int64(2), "survivor"})
			if old != nil {
				ksLifeDetachedRows(t, old, [][]any{{int64(1), "old"}, {int64(2), "survivor"}})
			}
		})
	})
}

func ksLifeDetachedRows(t *testing.T, rows *sql.Rows, expected [][]any) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 2 || columns[0] != "k" || columns[1] != "v" {
		t.Fatal("detached columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
		t.Fatal("detached metadata", types, err)
	}
	for _, want := range expected {
		if !rows.Next() {
			t.Fatal("detached row missing", rows.Err())
		}
		var k int64
		var v string
		if err := rows.Scan(&k, &v); err != nil || k != want[0] || v != want[1] {
			t.Fatal("detached values", k, v, want, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("detached tail", rows.Err())
	}
}

func TestReferencePgsurgeryKillPreparedTransactions(t *testing.T) {
	for _, mutation := range []string{"insert", "update_head", "update_predecessor", "intermediate", "delete"} {
		for _, timing := range []string{"kill_before_prepare", "kill_detached"} {
			for _, ending := range []string{"COMMIT", "ROLLBACK"} {
				t.Run(mutation+"/"+timing+"/"+ending, func(t *testing.T) {
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
						// Registered before PREPARE, including failure exits. Each
						// fixture completes its GID before schema cleanup.
						defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED 'ks_lifecycle_writer'")
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
								t.Fatal("prepared UPDATE reused predecessor")
							}
							if mutation == "update_predecessor" {
								target = old
							}
							if mutation == "intermediate" {
								next := freezeOne(t, p, writer, "UPDATE ks_part.heap SET v='successor' WHERE k=1 RETURNING k,v,ctid,xmin,xmax")
								if next.tid == target.tid || next.tid == old.tid {
									t.Fatal("prepared intermediate identity")
								}
							}
						}
						freezeRun(t, p, b, writer, fixture.Exec("INSERT INTO ks_part.heap VALUES(3,'prepared_peer')", 1))
						if timing == "kill_before_prepare" {
							ksLifeApply(t, p, b, writer, target.tid)
						}
						freezeRun(t, p, b, writer, fixture.Exec("PREPARE TRANSACTION 'ks_lifecycle_writer'", 0))
						freezeRun(t, p, b, c, fixture.Select("SELECT gid::text AS gid FROM pg_prepared_xacts WHERE gid='ks_lifecycle_writer'", []string{"gid"}, []string{"TEXT"}, [][]any{{"ks_lifecycle_writer"}}))
						if timing == "kill_detached" {
							ksLifeApply(t, p, b, c, target.tid)
						}
						pending := [][]any{{int64(2), "sentinel"}}
						if mutation == "update_head" || mutation == "intermediate" {
							pending = append([][]any{{int64(1), "old"}}, pending...)
						}
						ksLifeRows(t, p, b, c, pending...)
						freezeRun(t, p, b, c, fixture.Exec(ending+" PREPARED 'ks_lifecycle_writer'", 0))
						want := [][]any{{int64(2), "sentinel"}}
						if ending == "COMMIT" {
							want = append(want, []any{int64(3), "prepared_peer"})
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
						freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_prepared_xacts WHERE gid='ks_lifecycle_writer'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					})
				})
			}
		}
	}
}
