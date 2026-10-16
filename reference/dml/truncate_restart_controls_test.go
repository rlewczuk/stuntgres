package reference_test

import (
	"bytes"
	"context"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartControls(t *testing.T) {
	t.Run("CommittedDumpAndReplay", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateRestartSeed(t, f)
			truncateExec(t, f, f.A, "ALTER SEQUENCE t_id_seq START 33", 0)
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.A, q, held)
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			var private bytes.Buffer
			if !b.PG {
				if err := stuntgres.Dump(p.Ctx, p.Native, &private); err != nil {
					t.Fatal(err)
				}
			}
			truncateExec(t, f, f.A, "ROLLBACK", 0)
			if !b.PG {
				// Replay is an independent SQL witness of what Dump observed, not
				// a substring check of implementation-specific serialization.
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(private.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			truncateRows(t, f, f.B, "t", 1, 2)
			truncateRestartState(t, f, f.B, "t_id_seq", 2, true)
			// A newly prepared handle follows replay's new identity, not a stale
			// persistent held handle across Reset (which is intentionally fenced).
			x = truncateRestartRoute(t, p, f.A, q, held)
			truncateExec(t, f, x, q, 0)
			truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
			truncateRows(t, f, f.A, "t", 33)
			if !b.PG {
				dump := fixture.CheckDump(t, p)
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
					t.Fatal(err)
				}
			}
			truncateRows(t, f, f.B, "t", 33)
			truncateRestartState(t, f, f.B, "t_id_seq", 33, true)
			fresh, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			truncateRestartError(t, f, fresh, "SELECT currval('t_id_seq')", truncateWant("55000", `currval of sequence "t_id_seq" is not yet defined in this session`))
			truncateRestartError(t, f, fresh, "SELECT lastval()", truncateWant("55000", "lastval is not yet defined in this session"))
			truncateRestartValue(t, f, fresh, "SELECT nextval('t_id_seq')", "nextval", 34)
			q = "TRUNCATE t RESTART IDENTITY"
			truncateExec(t, f, truncateRestartRoute(t, p, fresh, q, held), q, 0)
			truncateRestartState(t, f, fresh, "t_id_seq", 33, false) // Ownership/options survived replay.
		})
	})
	for _, control := range []string{"Reset", "Restore"} {
		t.Run("NativeStagedFence/"+control, func(t *testing.T) {
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				truncateRestartSeed(t, f)
				q := "TRUNCATE t RESTART IDENTITY"
				x := truncateRestartRoute(t, p, f.A, q, held)
				var original []byte
				if !b.PG {
					original = fixture.CheckDump(t, p)
				}
				truncateExec(t, f, f.A, "BEGIN", 0)
				defer f.A.ExecContext(p.Ctx, "ROLLBACK")
				truncateExec(t, f, x, q, 0)
				truncateExec(t, f, f.A, "INSERT INTO t DEFAULT VALUES", 1)
				if b.PG {
					// PostgreSQL has no host replacement API; same SQL baseline.
					truncateExec(t, f, f.A, "ROLLBACK", 0)
					truncateRows(t, f, f.B, "t", 1, 2)
					return
				}
				if control == "Reset" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
				} else if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original)); err != nil {
					t.Fatal(err)
				}
				_, err := f.A.ExecContext(p.Ctx, "COMMIT")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				if control == "Reset" {
					d := truncateWant("42P01", `relation "t" does not exist`)
					d.Position = "16"
					truncateRestartError(t, f, f.B, "SELECT id FROM t", d)
					truncateExec(t, f, f.B, "CREATE TABLE t(id SERIAL)", 0)
					truncateExec(t, f, f.B, "INSERT INTO t DEFAULT VALUES", 1)
					truncateRows(t, f, f.B, "t", 1)
				} else {
					truncateRows(t, f, f.B, "t", 1, 2)
					truncateRestartValue(t, f, f.B, "SELECT nextval('t_id_seq')", "nextval", 3)
				}
			})
		})
		t.Run("NativeWaiterFence/"+control, func(t *testing.T) {
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				truncateRestartSeed(t, f)
				q := "TRUNCATE t RESTART IDENTITY"
				x := truncateRestartRoute(t, p, f.B, q, held)
				var original []byte
				if !b.PG {
					original = fixture.CheckDump(t, p)
				}
				holder := truncateRestartPID(t, p, b, f.A)
				waiter := truncateRestartPID(t, p, b, f.B)
				tx := truncateBegin(t, p, f.A, nil)
				truncateRestartState(t, f, tx, "t_id_seq", 2, true)
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, err := x.ExecContext(ctx, q)
					return fixture.RelationOutcome{Result: r, Err: err}
				})
				truncateRestartWait(t, p, b, holder, waiter, "t_id_seq", "AccessShareLock", "AccessExclusiveLock")
				if !b.PG {
					if control == "Reset" {
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
					} else if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(original)); err != nil {
						t.Fatal(err)
					}
				}
				tx.Rollback()
				o := join()
				if b.PG {
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					fixture.LockAffected(t, o.Result, 0)
					truncateRestartState(t, f, f.B, "t_id_seq", 1, false)
				} else {
					if o.Result != nil {
						t.Fatal("stale waiter published result")
					}
					fixture.TransactionOrdinary(t, o.Err, "stuntgres: catalog replaced before publication")
					if control == "Restore" {
						truncateRows(t, f, f.B, "t", 1, 2)
						truncateRestartState(t, f, f.B, "t_id_seq", 2, true)
					} else {
						truncateExec(t, f, f.B, "CREATE TABLE t(id SERIAL)", 0)
					}
					// Healthy replacement admission proves terminal lock cleanup.
					truncateExec(t, f, truncateRestartRoute(t, p, f.B, q, held), q, 0)
				}
			})
		})
	}
	t.Run("OwnedRowsAndMetadata", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE t(id SERIAL,note TEXT,payload BYTEA)", 0)
			truncateRun(t, f, f.A, fixture.Exec("INSERT INTO t(note,payload) VALUES('one',$1),('two',$2)", 2, []byte{0, 1, 255}, []byte{2, 3}))
			q := "TRUNCATE t RESTART IDENTITY"
			x := truncateRestartRoute(t, p, f.B, q, held)
			rows, err := f.A.QueryContext(p.Ctx, "SELECT id,note,payload FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"id", "note", "payload"}) {
				t.Fatalf("columns %v: %v", columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"INT4", "TEXT", "BYTEA"} {
				if types[i].DatabaseTypeName() != want {
					t.Fatalf("type %d: %s", i, types[i].DatabaseTypeName())
				}
			}
			consume := func() {
				t.Helper()
				for i, want := range [][]any{{int64(1), "one", []byte{0, 1, 255}}, {int64(2), "two", []byte{2, 3}}} {
					if !rows.Next() {
						t.Fatalf("retained row %d: %v", i, rows.Err())
					}
					var id int64
					var note string
					var payload []byte
					if err := rows.Scan(&id, &note, &payload); err != nil || !reflect.DeepEqual([]any{id, note, payload}, want) {
						t.Fatalf("row %d: %d %s %v: %v", i, id, note, payload, err)
					}
					payload[0] = 99
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatalf("retained tail: %v", rows.Err())
				}
				rows.Close()
			}
			if b.PG {
				consume()
			} // pq streaming holds AS; native output is eagerly owned.
			truncateExec(t, f, x, q, 0)
			if !b.PG {
				consume()
			}
			if !reflect.DeepEqual(columns, []string{"id", "note", "payload"}) || types[2].DatabaseTypeName() != "BYTEA" {
				t.Fatal("retained metadata changed")
			}
			truncateExec(t, f, f.B, "INSERT INTO t(note,payload) VALUES('new',NULL)", 1)
			truncateRows(t, f, f.B, "t", 1)
		})
	})
}
