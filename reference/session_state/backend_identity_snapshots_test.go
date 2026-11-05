package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func identityActivityCount(t *testing.T, p *fixture.Pair, o *sql.Conn, pid int64, want int64) {
	t.Helper()
	if n := backendCount(t, p, o, "SELECT count(*) FROM pg_stat_activity WHERE pid=$1", pid); n != want {
		t.Fatal("shared activity membership", pid, n, want)
	}
}

func identityPIDAbsent(t *testing.T, rows [][]any, pid int64) {
	t.Helper()
	for _, row := range rows {
		if row[1] == pid {
			t.Fatal("new PID in retained legacy cut", pid, rows)
		}
	}
}

func TestReferenceBackendIdentitySnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, activityFirst := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/activityFirst=%t", mode, activityFirst), func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
					admin := statActivityConn(t, p, b)
					peer := statActivityConn(t, p, b)
					pid := statActivityPID(t, p, peer)
					// Prepare before N exists; it must not establish an activity cut.
					held, err := o.PrepareContext(p.Ctx, identitySetSQL)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					e := fixture.HeldStatement{Executor: o, Statement: held, SQL: identitySetSQL}
					activityExec(t, p, o, "SET stats_fetch_consistency="+mode)
					activityExec(t, p, o, "BEGIN")
					defer o.ExecContext(context.Background(), "ROLLBACK")
					if activityFirst {
						identityActivityCount(t, p, o, pid, 1)
					}
					old := identityForPID(t, identitySet(t, p, b, e), pid)
					identityActivityCount(t, p, o, pid, 1)
					var stamp sql.NullTime
					if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp); err != nil || stamp.Valid {
						t.Fatal("activity-only access established cumulative timestamp", stamp, err)
					}
					newcomer := statActivityConn(t, p, b)
					newPID := statActivityPID(t, p, newcomer)
					identityPIDAbsent(t, identitySet(t, p, b, e), newPID)
					identityActivityCount(t, p, o, newPID, 0)
					// Demand a cumulative field: COUNT(*) could prune the capture.
					if n := backendCount(t, p, o, "SELECT tup_inserted FROM pg_stat_database WHERE datname=current_database()"); n < 0 {
						t.Fatal(n)
					}
					if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp); err != nil || mode == "snapshot" && !stamp.Valid || mode != "snapshot" && stamp.Valid {
						t.Fatal("cumulative consistency stamp contract", mode, stamp, err)
					}
					identityPIDAbsent(t, identitySet(t, p, b, e), newPID)
					identityActivityCount(t, p, o, newPID, 0)
					statActivitySignal(t, p, admin, pid, true)
					statActivityRetired(t, p, admin, pid)
					identityEqual(t, identityForPID(t, identitySet(t, p, b, e), pid), old)
					identityEqual(t, identityLookup(t, p, b, o, old[0]), old)
					identityActivityCount(t, p, o, pid, 1)
					statActivityClear(t, p, o)
					identityRetiredFresh(t, p, b, o, old)
					identityForPID(t, identitySet(t, p, b, e), newPID)
					identityActivityCount(t, p, o, pid, 0)
					identityActivityCount(t, p, o, newPID, 1)
					activityExec(t, p, o, "COMMIT")
					// Both end paths must independently invalidate a retained cut.
					for _, end := range []string{"COMMIT", "ROLLBACK"} {
						activityExec(t, p, o, "BEGIN")
						identitySet(t, p, b, e)
						later := statActivityConn(t, p, b)
						laterPID := statActivityPID(t, p, later)
						identityPIDAbsent(t, identitySet(t, p, b, e), laterPID)
						identityActivityCount(t, p, o, laterPID, 0)
						activityExec(t, p, o, end)
						identityForPID(t, identitySet(t, p, b, e), laterPID)
						identityActivityCount(t, p, o, laterPID, 1)
					}
				})
			})
		}
	}
	// A never-executed prepared recipe cannot capture membership at Prepare.
	t.Run("prepareBeforeBirth", func(t *testing.T) {
		activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
			s, err := o.PrepareContext(p.Ctx, identitySetSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			peer := statActivityConn(t, p, b)
			pid := statActivityPID(t, p, peer)
			e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identitySetSQL}
			identityForPID(t, identitySet(t, p, b, e), pid)
		})
	})
}
