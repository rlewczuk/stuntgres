package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func sslCoreRows(ctx context.Context, result core.Result) ([][]sqltype.Value, error) {
	if result.Rows == nil {
		return nil, fmt.Errorf("missing SSL result")
	}
	defer result.Rows.Close()
	var rows [][]sqltype.Value
	for {
		values := make([]sqltype.Value, len(result.Rows.Schema()))
		present, err := result.Rows.Next(ctx, values)
		if err != nil {
			return nil, err
		}
		if !present {
			break
		}
		rows = append(rows, values)
	}
	return rows, nil
}

func sslCoreValidate(rows [][]sqltype.Value) error {
	types := [...]sqltype.ID{sqltype.Int4, sqltype.Bool, sqltype.Text, sqltype.Text, sqltype.Int4, sqltype.Text, sqltype.Numeric, sqltype.Text}
	seen := make(map[int64]bool, len(rows))
	for _, row := range rows {
		if len(row) != len(types) {
			return fmt.Errorf("SSL width %d", len(row))
		}
		for i, value := range row {
			if value.Type() == nil || value.Type().ID() != types[i] || value.IsNull() != (i >= 2) {
				return fmt.Errorf("SSL typed cell %d: %v", i, value)
			}
		}
		pid, err := row[0].Integer()
		if err != nil || pid <= 0 || seen[pid] {
			return fmt.Errorf("SSL duplicate/invalid PID %d: %v", pid, err)
		}
		seen[pid] = true
		if ssl, err := row[1].Boolean(); err != nil || ssl {
			return fmt.Errorf("SSL invented transport %t: %v", ssl, err)
		}
	}
	return nil
}

func sslCoreWant(t *testing.T, rows [][]sqltype.Value, pid uint64, present bool) {
	t.Helper()
	if err := sslCoreValidate(rows); err != nil {
		t.Fatal(err)
	}
	want := 0
	if present {
		want = 1
	}
	if len(rows) != want {
		t.Fatal("SSL qualified membership", rows, pid, present)
	}
	if present {
		if got, err := rows[0][0].Integer(); err != nil || uint64(got) != pid {
			t.Fatal("SSL physical PID", got, pid, err)
		}
	}
}

func TestPGStatSSLSharedCapture(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, first := range []string{"pg_stat_activity", "pg_stat_ssl"} {
			for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
				t.Run(mode+"/"+first+"/"+terminal, func(t *testing.T) {
					e := engine.New()
					admin := enginetest.StartSession(t, e, nil)
					activityOwnerRun(t, e, admin, "CREATE DATABASE ssl_capture_peer")
					peer, err := e.SelectDatabase(t.Context(), "ssl_capture_peer")
					if err != nil {
						t.Fatal(err)
					}
					target := enginetest.StartSession(t, peer, nil)
					monitor := enginetest.StartSession(t, e, nil)
					activityOwnerRun(t, peer, target, "CREATE TABLE ssl_canonical(v integer)")
					peer.Mu.Lock()
					peer.Visibility.SetLastAssignedXIDLocked(1<<32 | 40)
					peer.Mu.Unlock()
					// The public full-XID snapshot frontier advances on genuine
					// completion, not merely on the assignment seeding seam. Commit
					// a high-epoch row before the target accepts its RR snapshot.
					writer := enginetest.StartSession(t, peer, nil)
					activityOwnerRun(t, peer, writer, "BEGIN", "INSERT INTO ssl_canonical VALUES(5)", "COMMIT")
					activityOwnerRun(t, peer, target, "BEGIN ISOLATION LEVEL REPEATABLE READ", "INSERT INTO ssl_canonical VALUES(7)")
					// Own private rows need not demand committed snapshot access.
					// Reading the other writer's actual row establishes and advertises
					// the accepted public horizon instead.
					committed := activityOwnerValues(t, peer, target, "SELECT v FROM ssl_canonical WHERE v=5")
					if len(committed) != 1 || len(committed[0]) != 1 {
						t.Fatal("missing committed snapshot witness", committed)
					}
					if value, err := committed[0][0].Integer(); err != nil || value != 5 {
						t.Fatal("actual committed snapshot row", value, err)
					}
					activityOwnerRun(t, e, monitor, "SET stats_fetch_consistency="+mode, "BEGIN")
					ref := target.Transactions.Ref
					peer.Mu.Lock()
					entry, exists := peer.Visibility.Entries[ref]
					assigned := peer.Visibility.AssignedXIDLocked(ref)
					snapshot := target.Transactions.CurrentSnapshot
					frontier := peer.Visibility.LastAssignedXID()
					frontierSnapshot, err := peer.Visibility.CurrentSnapshotLocked(ref)
					peer.Mu.Unlock()
					if err != nil {
						t.Fatal("canonical snapshot frontier before monitoring", err)
					}
					if !exists || entry.FullXID != 1<<32|42 || assigned != 42 || snapshot == nil || snapshot.Xmin() != 1<<32|42 || entry.ActivityFullXmin != snapshot.Xmin() {
						t.Fatal("actual high epoch canonical transaction", entry, assigned, snapshot)
					}
					// Only target-database authority is invariant: the observer's
					// own database may legitimately bookkeep its monitoring commands.
					assertUnchanged := func(phase string) {
						t.Helper()
						peer.Mu.Lock()
						after, exists := peer.Visibility.Entries[ref]
						now := peer.Visibility.LastAssignedXID()
						nowSnapshot, err := peer.Visibility.CurrentSnapshotLocked(ref)
						peer.Mu.Unlock()
						if err != nil {
							t.Fatal(phase, "canonical snapshot frontier after monitoring", err)
						}
						if !exists || after != entry || now != frontier || !reflect.DeepEqual(nowSnapshot, frontierSnapshot) {
							t.Fatal(phase, "monitoring mutated target visibility authority", entry, after, frontier, now, frontierSnapshot, nowSnapshot)
						}
					}
					pid := target.SessionToken.ID()
					query := fmt.Sprintf("SELECT * FROM pg_stat_ssl WHERE pid=%d", pid)
					activityQuery := fmt.Sprintf("SELECT datname,pid,backend_xid,backend_xmin FROM pg_stat_activity WHERE pid=%d", pid)
					if first == "pg_stat_activity" {
						activityOwnerValues(t, e, monitor, activityQuery)
					}
					ast, description := enginetest.PreparedDescription(t, e, monitor, query)
					capture := func() core.Result {
						result, err := enginetest.PreparedExecute(e, monitor, ast, &description)
						if err != nil {
							t.Fatal(err)
						}
						return result
					}
					oldResult := capture()
					defer oldResult.Rows.Close()
					assertUnchanged("SSL capture")
					oldSchema := append([]catalog.Column(nil), oldResult.Rows.Schema()...)
					rows := activityOwnerValues(t, e, monitor, activityQuery)
					assertUnchanged("subsequent activity projection")
					if len(rows) != 1 {
						t.Fatal("canonical activity membership", rows)
					}
					if database, err := rows[0][0].Text(); err != nil || database != "ssl_capture_peer" {
						t.Fatal("actual target database", database, err)
					}
					if xid, err := rows[0][2].XID(); err != nil || xid != assigned {
						t.Fatal("SSL-first canonical XID", xid, assigned, err)
					}
					if xmin, err := rows[0][3].XID(); err != nil || xmin != uint32(entry.ActivityFullXmin) {
						t.Fatal("SSL-first canonical xmin", xmin, entry.ActivityFullXmin, err)
					}
					stamp := activityOwnerValues(t, e, monitor, "SELECT pg_stat_get_snapshot_timestamp()")
					if len(stamp) != 1 || !stamp[0][0].IsNull() {
						t.Fatal("activity/SSL created cumulative snapshot", stamp)
					}
					activityOwnerRun(t, peer, target, "ROLLBACK")
					target.RequestEnd(session.CleanClose)
					if err := target.WaitEnd(t.Context()); err != nil {
						t.Fatal(err)
					}
					newcomer := enginetest.StartSession(t, peer, nil)
					if newcomer.SessionToken == target.SessionToken {
						t.Fatal("reused physical identity")
					}
					sslCoreWant(t, activityOwnerValues(t, e, monitor, query), uint64(pid), true)
					newQuery := fmt.Sprintf("SELECT * FROM pg_stat_ssl WHERE pid=%d", newcomer.SessionToken.ID())
					sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(newcomer.SessionToken.ID()), false)
					if got := activityOwnerValues(t, e, monitor, activityQuery); !reflect.DeepEqual(got, rows) {
						t.Fatal("shared retained cut changed canonical facts", got, rows)
					}
					activityOwnerRun(t, e, monitor, "SELECT pg_stat_clear_snapshot()")
					sslCoreWant(t, activityOwnerValues(t, e, monitor, query), uint64(pid), false)
					sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(newcomer.SessionToken.ID()), true)
					if got := activityOwnerValues(t, e, monitor, activityQuery); len(got) != 0 {
						t.Fatal("clear did not invalidate shared activity cut", got)
					}
					newcomer.RequestEnd(session.CleanClose)
					if err := newcomer.WaitEnd(t.Context()); err != nil {
						t.Fatal(err)
					}
					successor := enginetest.StartSession(t, peer, nil)
					terminalQuery := fmt.Sprintf("SELECT * FROM pg_stat_ssl WHERE pid=%d", successor.SessionToken.ID())
					sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(newcomer.SessionToken.ID()), true)
					sslCoreWant(t, activityOwnerValues(t, e, monitor, terminalQuery), uint64(successor.SessionToken.ID()), false)
					activityOwnerRun(t, e, monitor, terminal, "BEGIN")
					sslCoreWant(t, activityOwnerValues(t, e, monitor, newQuery), uint64(newcomer.SessionToken.ID()), false)
					sslCoreWant(t, activityOwnerValues(t, e, monitor, terminalQuery), uint64(successor.SessionToken.ID()), true)
					activityOwnerRun(t, e, monitor, "ROLLBACK")
					if !reflect.DeepEqual(oldResult.Rows.Schema(), oldSchema) {
						t.Fatal("clear/end mutated retained SSL schema")
					}
					old, err := sslCoreRows(t.Context(), oldResult)
					if err != nil {
						t.Fatal(err)
					}
					sslCoreWant(t, old, uint64(pid), true)
					// Result release and later projections cannot change owned scalars.
					frozen := append([]sqltype.Value(nil), old[0]...)
					activityOwnerValues(t, e, monitor, newQuery)
					if !reflect.DeepEqual(old[0], frozen) {
						t.Fatal("released result backing mutated")
					}
				})
			}
		}
	}
}
