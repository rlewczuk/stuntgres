package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInheritanceDMLContention(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		for _, lane := range []string{"refresh", "predicate", "peer_delete", "later_insert", "repeatable", "cancel"} {
			t.Run(op+"_"+lane, func(t *testing.T) {
				inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
					var target fixture.Executor = c
					var reader *sql.Tx
					if lane == "repeatable" {
						var err error
						reader, err = c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
						if err != nil {
							t.Fatal(err)
						}
						defer reader.Rollback()
						target = reader
						fixture.QueryLockRows(t, p, b, reader, "SELECT v FROM p WHERE id=2", prepared, "v", 20)
					}
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					action := "UPDATE child SET v=30 WHERE id=2"
					if lane == "predicate" {
						action = "UPDATE child SET id=3,v=30 WHERE id=2"
					}
					if lane == "peer_delete" {
						action = "DELETE FROM child WHERE id=2"
					}
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec(action, 1))
					// ONLY does not conflict with the child's genuine row lock.
					if reader == nil {
						fixture.UpdateCoreExec(t, p.Ctx, c, "UPDATE ONLY p SET v=v", prepared, 1)
					}
					ap, bp := 0, 0
					if b.PG {
						ap, bp = fixture.RowPIDs(t, p.Ctx, a, c)
					}
					query := "UPDATE p SET v=v+1 WHERE id=2 RETURNING v"
					if op == "delete" {
						query = "DELETE FROM p WHERE id=2 RETURNING v"
					}
					if lane == "cancel" {
						query = "UPDATE p SET v=v+1 WHERE id>0 RETURNING v"
						if op == "delete" {
							query = "DELETE FROM p WHERE id>0 RETURNING v"
						}
					}
					if lane == "later_insert" {
						query = "UPDATE p SET v=v+1 WHERE id>=2 RETURNING v"
						if op == "delete" {
							query = "DELETE FROM p WHERE id>=2 RETURNING v"
						}
					}
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						return fixture.RowQuery(ctx, target, query, prepared, b.Recorder.RawBackend)
					})
					inheritanceQueryWait(t, p, b, ap, bp)
					if lane == "later_insert" {
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						fixture.UpdateCoreExec(t, p.Ctx, peer, "INSERT INTO child VALUES(false,3,90,'later')", prepared, 1)
						if err = peer.Close(); err != nil {
							t.Fatal(err)
						}
					}
					if lane == "cancel" {
						if b.PG {
							var ok bool
							if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", bp).Scan(&ok); err != nil || !ok {
								t.Fatal(ok, err)
							}
						} else {
							cancel()
						}
						prefix := []int64{11}
						if op == "delete" {
							prefix = []int64{10}
						}
						failure := fixture.QueryLockErrorOutcome(t, await(), b.PG, "v", prefix)
						if b.PG {
							verb := "updating"
							if op == "delete" {
								verb = "deleting"
							}
							fixture.AssertDiagnostic(t, failure, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request", Context: "while " + verb + " tuple (0,1) in relation \"child\""})
						} else if !errors.Is(failure, context.Canceled) {
							t.Fatal(failure)
						}
						if err := holder.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id", prepared, "v", 10, 20)
						fixture.UpdateCoreExec(t, p.Ctx, c, "UPDATE p SET v=v+1", prepared, 2)
						return
					}
					if err := holder.Commit(); err != nil {
						t.Fatal(err)
					}
					out := await()
					switch lane {
					case "repeatable":
						failure := fixture.QueryLockErrorOutcome(t, out, b.PG, "v", nil)
						fixture.AssertDiagnostic(t, failure, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update"})
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
					case "predicate", "peer_delete":
						fixture.RowValues(t, out)
					default:
						v := int64(31)
						if op == "delete" {
							v = 30
						}
						fixture.RowValues(t, out, v)
					}
					expected := []int64{10}
					if lane == "predicate" || lane == "repeatable" {
						expected = append(expected, 30)
					}
					if (lane == "refresh" || lane == "later_insert") && op == "update" {
						expected = append(expected, 31)
					}
					if lane == "later_insert" {
						expected = append(expected, 90)
					}
					fixture.QueryLockRows(t, p, b, c, "SELECT v FROM p ORDER BY id", prepared, "v", expected...)
				})
			})
		}
	}
	// The inverse direction proves inherited mutation owns the physical child row,
	// not a surrogate row on p with an accidentally equal RowID.
	inheritanceQueryLocks(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, c *sql.Conn, prepared bool) {
		holder := fixture.QueryLockBegin(t, p.Ctx, a)
		fixture.UpdateCoreExec(t, p.Ctx, holder, "UPDATE p SET v=v+1 WHERE id=2", prepared, 1)
		fixture.QueryLockNowait(t, p, b, c, "SELECT v FROM child WHERE id=2 FOR UPDATE NOWAIT", prepared, "child")
		fixture.QueryLockRows(t, p, b, c, "SELECT v FROM ONLY p FOR UPDATE NOWAIT", prepared, "v", 10)
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.QueryLockRows(t, p, b, c, "SELECT v FROM child FOR UPDATE NOWAIT", prepared, "v", 20)
	})
}
