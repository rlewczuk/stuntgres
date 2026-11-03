package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatsXactBlocksLifetime(t *testing.T) {
	for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
		for _, write := range []bool{false, true} {
			label := terminal + "_readonly"
			if write {
				label = terminal + "_insert"
			}
			t.Run(label, func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, setup *sql.Conn) {
					statsCreate(t, p, b, setup, "xact_terminal")
					statsExec(t, p, b, setup, "INSERT INTO xact_terminal VALUES(1),(2)", 2)
					oid := xactBlockOID(t, p, setup, "xact_terminal")
					// setup remains borrowed, so this is a genuinely fresh physical
					// writer with no earlier publication throttle to prime.
					c, retire := statsReportingConn(t, p, b)
					defer retire()
					xactBlockBegin(t, p, b, c)
					defer c.ExecContext(context.Background(), "ROLLBACK")
					stmt, err := c.PrepareContext(p.Ctx, xactBlockSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					xactBlockWant(t, xactBlockRead(t, p, c, stmt, oid), xactBlockPair{})
					xactBlockRows(t, p, b, c, "SELECT id FROM xact_terminal ORDER BY id", 1, 2)
					xactBlockAdvanced(t, b, xactBlockPair{}, xactBlockRead(t, p, c, stmt, oid))
					if write {
						statsExec(t, p, b, c, "INSERT INTO xact_terminal VALUES(3)", 1)
						xactBlockRows(t, p, b, c, "SELECT id FROM xact_terminal ORDER BY id", 1, 2, 3)
					}
					xactBlockTerminal(t, p, b, c, terminal)
					statsExec(t, p, b, c, "BEGIN", 0)
					xactBlockWant(t, xactBlockRead(t, p, c, stmt, oid), xactBlockPair{})
					statsExec(t, p, b, c, "ROLLBACK", 0)
					want := []int64{1, 2}
					if write && terminal == "COMMIT" {
						want = append(want, 3)
					}
					xactBlockRows(t, p, b, setup, "SELECT id FROM xact_terminal ORDER BY id", want...)
				})
			})
		}
	}
	t.Run("SavepointAndFailedRead", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			statsCreate(t, p, b, c, "xact_savepoint")
			statsExec(t, p, b, c, "INSERT INTO xact_savepoint VALUES(1),(2)", 2)
			oid := xactBlockOID(t, p, c, "xact_savepoint")
			xactBlockBegin(t, p, b, c)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			statsExec(t, p, b, c, "SAVEPOINT xact_mark", 0)
			before := xactBlockRead(t, p, c, nil, oid)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_savepoint ORDER BY id", 1, 2)
			first := xactBlockRead(t, p, c, nil, oid)
			xactBlockAdvanced(t, b, before, first)
			statsExec(t, p, b, c, "ROLLBACK TO xact_mark", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), first)
			rows, err := c.QueryContext(p.Ctx, "SELECT id/(id-id) FROM xact_savepoint")
			if rows != nil {
				// pq may expose RowDescription before a row-evaluation failure;
				// native may instead fail its eager Query. Neither may yield a row.
				if rows.Next() {
					rows.Close()
					t.Fatal("failed read yielded a value")
				}
				err = rows.Err()
				rows.Close()
			}
			want := activityDiagnostic("22012", "division by zero", 0)
			fixture.AssertDiagnostic(t, err, &want)
			statsExec(t, p, b, c, "ROLLBACK TO xact_mark", 0)
			afterError := xactBlockRead(t, p, c, nil, oid)
			xactBlockAdvanced(t, b, first, afterError)
			statsClear(t, p, b, c)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), afterError)
			statsExec(t, p, b, c, "INSERT INTO xact_savepoint VALUES(3)", 1)
			statsExec(t, p, b, c, "ROLLBACK TO xact_mark", 0)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_savepoint ORDER BY id", 1, 2)
		})
	})
	t.Run("PrivateAbortDropAndRecreate", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			xactBlockBegin(t, p, b, c)
			defer c.ExecContext(context.Background(), "ROLLBACK")
			statsExec(t, p, b, c, "CREATE TABLE xact_aborted(id integer)", 0)
			statsExec(t, p, b, c, "INSERT INTO xact_aborted VALUES(7)", 1)
			aborted := xactBlockOID(t, p, c, "xact_aborted")
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_aborted ORDER BY id", 7)
			if xactBlockRead(t, p, c, nil, aborted).fetched == 0 {
				t.Fatal("private read was not collected")
			}
			xactBlockTerminal(t, p, b, c, "ROLLBACK")
			statsExec(t, p, b, c, "BEGIN", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, aborted), xactBlockPair{})
			statsExec(t, p, b, c, "ROLLBACK", 0)
			statsCreate(t, p, b, c, "xact_identity")
			statsExec(t, p, b, c, "INSERT INTO xact_identity VALUES(1),(2)", 2)
			old := xactBlockOID(t, p, c, "xact_identity")
			statsExec(t, p, b, c, "BEGIN", 0)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_identity ORDER BY id", 1, 2)
			before := xactBlockRead(t, p, c, nil, old)
			statsExec(t, p, b, c, "SAVEPOINT before_drop", 0)
			statsExec(t, p, b, c, "DROP TABLE xact_identity", 0)
			statsExec(t, p, b, c, "ROLLBACK TO before_drop", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, old), before)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_identity ORDER BY id", 1, 2)
			statsExec(t, p, b, c, "DROP TABLE xact_identity", 0)
			xactBlockTerminal(t, p, b, c, "COMMIT")
			statsExec(t, p, b, c, "BEGIN", 0)
			xactBlockWant(t, xactBlockRead(t, p, c, nil, old), xactBlockPair{})
			statsExec(t, p, b, c, "CREATE TABLE xact_identity(id integer)", 0)
			statsExec(t, p, b, c, "INSERT INTO xact_identity VALUES(9)", 1)
			successor := xactBlockOID(t, p, c, "xact_identity")
			if successor == old {
				t.Fatal("drop/recreate reused live identity")
			}
			base := xactBlockRead(t, p, c, nil, successor)
			xactBlockRows(t, p, b, c, "SELECT id FROM xact_identity ORDER BY id", 9)
			xactBlockAdvanced(t, b, base, xactBlockRead(t, p, c, nil, successor))
			xactBlockWant(t, xactBlockRead(t, p, c, nil, old), xactBlockPair{})
		})
	})
	t.Run("TempOwnerRetirement", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
			owner, retire := statsReportingConn(t, p, b)
			statsExec(t, p, b, owner, "BEGIN", 0)
			statsExec(t, p, b, owner, "CREATE TEMP TABLE xact_owned_temp(id integer)", 0)
			statsExec(t, p, b, owner, "INSERT INTO xact_owned_temp VALUES(7)", 1)
			oid := xactBlockOID(t, p, owner, "xact_owned_temp")
			xactBlockRows(t, p, b, owner, "SELECT id FROM xact_owned_temp ORDER BY id", 7)
			if xactBlockRead(t, p, owner, nil, oid).fetched == 0 {
				t.Fatal("TEMP read was excluded")
			}
			xactBlockWant(t, xactBlockRead(t, p, observer, nil, oid), xactBlockPair{})
			statsExec(t, p, b, owner, "COMMIT", 0)
			retire()
			xactBlockWant(t, xactBlockRead(t, p, observer, nil, oid), xactBlockPair{})
		})
	})
	t.Run("NativeGenerationControls", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				return
			} // Reset/Restore are native public controls, not SQL substitutes.
			statsCreate(t, p, b, c, "xact_control")
			statsExec(t, p, b, c, "INSERT INTO xact_control VALUES(1),(2)", 2)
			oid := xactBlockOID(t, p, c, "xact_control")
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, restore := range []bool{false, true} {
				if restore {
					statsExec(t, p, b, c, "CREATE TABLE xact_control(id integer)", 0)
					statsExec(t, p, b, c, "INSERT INTO xact_control VALUES(9)", 1)
					oid = xactBlockOID(t, p, c, "xact_control")
				}
				// Keep actual detached rows alive across the generation change.
				retained, err := c.QueryContext(p.Ctx, "SELECT id FROM xact_control ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				if restore {
					err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
				} else {
					err = stuntgres.Reset(p.Ctx, p.Native)
				}
				if err != nil {
					retained.Close()
					t.Fatal(err)
				}
				want := []int64{1, 2}
				if restore {
					want = []int64{9}
				}
				for _, value := range want {
					if !retained.Next() {
						retained.Close()
						t.Fatal("retained row missing", retained.Err())
					}
					var got int64
					if err := retained.Scan(&got); err != nil || got != value {
						retained.Close()
						t.Fatal("retained row", got, value, err)
					}
				}
				if retained.Next() || retained.Err() != nil {
					retained.Close()
					t.Fatal("retained cardinality", retained.Err())
				}
				retained.Close()
				xactBlockWant(t, xactBlockRead(t, p, c, nil, oid), xactBlockPair{})
				if restore {
					newOID := xactBlockOID(t, p, c, "xact_control")
					statsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(context.Background(), "ROLLBACK")
					base := xactBlockRead(t, p, c, nil, newOID)
					xactBlockRows(t, p, b, c, "SELECT id FROM xact_control ORDER BY id", 1, 2)
					xactBlockAdvanced(t, b, base, xactBlockRead(t, p, c, nil, newOID))
					statsExec(t, p, b, c, "ROLLBACK", 0)
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='xact_control'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			}
		})
	})
}
