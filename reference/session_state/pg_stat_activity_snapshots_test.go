package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivitySnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
				holder := statActivityConn(t, p, b)
				peer := statActivityConn(t, p, b)
				pid := statActivityPID(t, p, peer)
				hpid := statActivityPID(t, p, holder)
				activityExec(t, p, o, "CREATE TABLE activity_cut(v integer)")
				activityExec(t, p, o, "SET stats_fetch_consistency="+mode)
				held, err := o.PrepareContext(p.Ctx, statActivitySelect)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				activityExec(t, p, holder, "BEGIN")
				defer holder.ExecContext(context.Background(), "ROLLBACK")
				activityExec(t, p, holder, "LOCK TABLE activity_cut IN ACCESS EXCLUSIVE MODE")
				activityExec(t, p, peer, "BEGIN")
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				q := "LOCK TABLE activity_cut IN ACCESS EXCLUSIVE MODE"
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					_, err := peer.ExecContext(ctx, q)
					return fixture.RelationOutcome{Err: err}
				})
				joined := false
				defer func() {
					cancel()
					if !joined {
						await()
					}
				}()
				statActivityBlocker(t, p, o, pid, hpid)
				activityExec(t, p, o, "BEGIN")
				defer o.ExecContext(context.Background(), "ROLLBACK")
				captured := statActivityGet(t, p, o, pid, true)
				if captured.text(16) != "active" || captured.text(15) != "relation" {
					t.Fatal("capture did not witness waiter", captured)
				}
				statsNoTimestamp(t, p, o, nil)
				retained, err := held.QueryContext(p.Ctx, pid)
				if err != nil {
					t.Fatal(err)
				}
				statActivitySignal(t, p, holder, pid, false)
				out := await()
				joined = true
				fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
				frozen, ok := statActivityRead(t, retained)
				if !ok || frozen.text(15) != "relation" {
					t.Fatal("public Rows was live", frozen)
				}
				rows, err := held.QueryContext(p.Ctx, pid)
				if err != nil {
					t.Fatal(err)
				}
				cached, ok := statActivityRead(t, rows)
				if !ok {
					t.Fatal("cached row missing")
				}
				for _, i := range []int{10, 11, 12, 13, 16, 17, 18, 19, 20} {
					if cached.text(i) != captured.text(i) {
						t.Fatalf("%s mode changed cached %s", mode, statActivityNames[i])
					}
				}
				statActivityIdleWait(t, b.PG, cached)
				fresh := statActivityGet(t, p, o, pid, true)
				statActivityState(t, b.PG, fresh, "idle in transaction (aborted)", q)
				statsNoTimestamp(t, p, o, nil)
				statActivitySignal(t, p, holder, pid, true)
				statActivityRetired(t, p, holder, pid)
				// Cached membership persists after real retirement, with no live wait.
				rows, err = held.QueryContext(p.Ctx, pid)
				if err != nil {
					t.Fatal(err)
				}
				cached, ok = statActivityRead(t, rows)
				if !ok || cached.text(16) != fresh.text(16) || cached[15] != nil {
					t.Fatal("retirement changed cut", cached)
				}
				newcomer := statActivityConn(t, p, b)
				newpid := statActivityPID(t, p, newcomer)
				rows, err = held.QueryContext(p.Ctx, newpid)
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := statActivityRead(t, rows); ok {
					t.Fatal("new member entered old activity cut")
				}
				statActivityAbsent(t, p, o, pid)
				if r := statActivityGet(t, p, o, newpid, false); r.text(2) == "<NULL>" {
					t.Fatal("clear did not add live member")
				}
				activityExec(t, p, o, "COMMIT")
				activityExec(t, p, o, "BEGIN")
				if r := statActivityGet(t, p, o, newpid, false); r.text(2) == "<NULL>" {
					t.Fatal("transaction end failed to refresh")
				}
				activityExec(t, p, o, "ROLLBACK")
				activityExec(t, p, holder, "ROLLBACK")
			})
		})
	}
}
