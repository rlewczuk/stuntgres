package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgsurgeryKillConcurrency(t *testing.T) {
	ksLifeWaitAuthorityCases(t)
	ksLifeRenameWaitCase(t)
	ksLifeReplacementWaitCases(t)
	for _, ending := range []string{"release", "cancel"} {
		t.Run("ax_"+ending, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("INSERT INTO ks_part.heap VALUES(2,'survivor')", 1))
				holder := freezeConn(t, p, b)
				defer holder.Close()
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				st, err := c.PrepareContext(p.Ctx, killSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'ks_part.heap'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				waiterPID, holderPID := 0, 0
				if b.PG {
					waiterPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
				}
				freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE", 0))
				call := func(ctx context.Context) fixture.RelationOutcome {
					rows, err := st.QueryContext(ctx, "ks_part.heap", freezeTIDs(a.tid))
					return fixture.RelationOutcome{Rows: rows, Err: err}
				}
				if ending == "cancel" {
					fixture.BinaryConversionActiveCancelAfter(t, p, b, c, killSQL, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "RowExclusiveLock")
						}
					}, call)
				} else {
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					finished := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome { defer close(finished); return call(ctx) })
					joined := false
					defer func() {
						if !joined {
							cancel()
							out := await()
							if out.Rows != nil {
								out.Rows.Close()
							}
						}
					}()
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, "RowExclusiveLock", finished)
					freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
					out := await()
					joined = true
					ksLifeVoidRows(t, out.Rows, out.Err)
				}
				freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
				// Cancellation may retire pq's transport. A fresh lease proves
				// healthy subsequent use and no publication before AX admission.
				reader := freezeConn(t, p, b)
				defer reader.Close()
				want := [][]any{{int64(2), "survivor"}}
				if ending == "cancel" {
					want = append([][]any{{int64(1), "target"}}, want...)
				}
				ksLifeRows(t, p, b, reader, want...)
				freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
				freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				freezeRun(t, p, b, holder, fixture.Exec("ROLLBACK", 0))
			})
		})
	}
	for _, prior := range []string{"none", "RX", "AX"} {
		t.Run("release_only_own_contribution_"+prior, func(t *testing.T) {
			ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
				freezeRun(t, p, b, c, fixture.Exec("CREATE TABLE ks_part.unrelated(k integer)", 0))
				peer := freezeConn(t, p, b)
				defer peer.Close()
				defer peer.ExecContext(p.Ctx, "ROLLBACK")
				freezeRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				freezeRun(t, p, b, c, fixture.Exec("LOCK TABLE ks_part.unrelated IN ACCESS EXCLUSIVE MODE", 0))
				freezeRun(t, p, b, c, freezeVoid("SELECT pg_advisory_xact_lock(735193::bigint) AS repaired"))
				if prior != "none" {
					mode := "ROW EXCLUSIVE"
					if prior == "AX" {
						mode = "ACCESS EXCLUSIVE"
					}
					freezeRun(t, p, b, c, fixture.Exec("LOCK TABLE ks_part.heap IN "+mode+" MODE", 0))
				}
				ksLifeApply(t, p, b, c, a.tid)
				freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=pg_backend_pid() AND granted AND ((locktype='relation' AND relation='ks_part.unrelated'::regclass AND mode='AccessExclusiveLock') OR locktype='advisory')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				var held int64
				if prior != "none" {
					held = 1
				}
				freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_locks WHERE pid=pg_backend_pid() AND granted AND locktype='relation' AND relation='ks_part.heap'::regclass AND mode IN ('RowExclusiveLock','AccessExclusiveLock')", []string{"n"}, []string{"INT8"}, [][]any{{held}}))
				freezeRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
				if prior == "none" {
					freezeRun(t, p, b, peer, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				} else {
					freezeRun(t, p, b, peer, fixture.ImplicitError("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "ks_part.heap"`)))
				}
				freezeRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
				freezeRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
				freezeRun(t, p, b, peer, fixture.ImplicitError("LOCK TABLE ks_part.unrelated IN ACCESS SHARE MODE NOWAIT", fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "ks_part.unrelated"`)))
				freezeRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
				freezeRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				freezeRun(t, p, b, peer, fixture.Exec("BEGIN", 0))
				freezeRun(t, p, b, peer, fixture.Exec("LOCK TABLE ks_part.heap,ks_part.unrelated IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				freezeRun(t, p, b, peer, fixture.Exec("ROLLBACK", 0))
				ksLifeRows(t, p, b, peer)
			})
		})
	}
	t.Run("native_stale_storage_wait", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			if b.PG {
				return
			} // Native captured incarnation fencing is not a public TID token.
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'old') RETURNING k,v,ctid,xmin,xmax")
			holder := freezeConn(t, p, b)
			defer holder.Close()
			defer holder.ExecContext(p.Ctx, "ROLLBACK")
			freezeRun(t, p, b, holder, fixture.Exec("BEGIN", 0))
			freezeRun(t, p, b, holder, fixture.Exec("LOCK TABLE ks_part.heap IN ACCESS EXCLUSIVE MODE", 0))
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				rows, err := c.QueryContext(ctx, killSQL, "ks_part.heap", freezeTIDs(a.tid))
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			joined := false
			defer func() {
				if !joined {
					cancel()
					out := await()
					if out.Rows != nil {
						out.Rows.Close()
					}
				}
			}()
			fixture.RelationLockPending(t, p, b, 0, 0, 0, "RowExclusiveLock", finished)
			freezeRun(t, p, b, holder, fixture.Exec("TRUNCATE ks_part.heap", 0))
			freezeRun(t, p, b, holder, fixture.Exec("INSERT INTO ks_part.heap VALUES(1,'new_incarnation')", 1))
			freezeRun(t, p, b, holder, fixture.Exec("COMMIT", 0))
			out := await()
			joined = true
			if out.Rows != nil {
				out.Rows.Close()
			}
			// Existing ids.ErrCatalogReplaced is deliberately an ordinary Go
			// error, not a fabricated PostgreSQL SQLSTATE.
			var state interface{ SQLState() string }
			if out.Err == nil || out.Err.Error() != "stuntgres: catalog replaced before publication" || errors.As(out.Err, &state) {
				t.Fatal("stale incarnation error", out.Err)
			}
			ksLifeRows(t, p, b, holder, []any{int64(1), "new_incarnation"})
		})
	})
	t.Run("native_post_completion_notice_cancel", func(t *testing.T) {
		ksCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			if b.PG {
				return
			} // Neither native adapter handlers nor pq receipt are group interrupts.
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, false, &notices, fixture.NoticeReferenceOption{Handler: func(fixture.Diagnostic) { cancel() }})
			b.Recorder = rec
			a := freezeOne(t, p, c, "INSERT INTO ks_part.heap VALUES(1,'target') RETURNING k,v,ctid,xmin,xmax")
			// Operation notices are buffered. The adapter invokes this handler
			// after execution/lease release, so cancellation cannot stop a suffix.
			// Exact between-group cancellation is an owning-context invariant.
			rows, err := c.QueryContext(ctx, killSQL, "ks_part.heap", freezeTIDs(a.tid, "(4294967293,1)", "(4294967294,1)"))
			if rows != nil {
				rows.Close()
			}
			if err != nil || !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("post-completion notice cancellation", err, ctx.Err())
			}
			want := []fixture.Diagnostic{
				{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22023", Message: `skipping block 4294967293 for relation "heap" because the block number is out of range`},
				{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "22023", Message: `skipping block 4294967294 for relation "heap" because the block number is out of range`},
			}
			if len(notices) != len(want) || notices[0] != want[0] || notices[1] != want[1] {
				t.Fatalf("completed-operation notices %#v, want %#v", notices, want)
			}
			ksLifeRows(t, p, b, c)
		})
	})
}

func ksLifeVoidRows(t *testing.T, rows *sql.Rows, err error) {
	t.Helper()
	if rows != nil {
		defer rows.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil {
		t.Fatal("missing VOID rows")
	}
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "killed" {
		t.Fatal("VOID columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "VOID" {
		t.Fatal("VOID metadata", types, err)
	}
	if !rows.Next() {
		t.Fatal("missing VOID value", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil || raw == nil || fixture.Normalize(raw) != "" {
		t.Fatal("VOID value", raw, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("VOID tail", rows.Err())
	}
}
