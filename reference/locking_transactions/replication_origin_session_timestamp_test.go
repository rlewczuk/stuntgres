package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func originTimeRegistration(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) int64 {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		c.ExecContext(ctx, "ROLLBACK")
		var selected bool
		if err := c.QueryRowContext(ctx, "SELECT pg_replication_origin_session_is_setup()").Scan(&selected); err != nil {
			t.Error(err)
		} else if selected {
			if _, err := c.ExecContext(ctx, "SELECT pg_replication_origin_session_reset()"); err != nil {
				t.Error(err)
			}
		}
		if _, err := b.DB.ExecContext(ctx, "SELECT pg_replication_origin_drop($1::text)", name); err != nil {
			t.Error(err)
		}
	})
	var id int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_replication_origin_create($1::text)", name).Scan(&id); err != nil || id <= 0 || id >= 65535 {
		t.Fatal(id, err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT pg_replication_origin_session_setup($1::text)", 1, name))
	return id
}
func originTimeLast(t *testing.T, p *fixture.Pair, c *sql.Conn) commitTimeRecord {
	t.Helper()
	var r commitTimeRecord
	if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_last_committed_xact()").Scan(&r.xid, &r.stamp, &r.origin); err != nil {
		t.Fatal(err)
	}
	return r
}
func originTimeWrite(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn, n int) commitTimeRecord {
	t.Helper()
	fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
	fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO origin_time_rows VALUES($1)", 1, n))
	var xid string
	if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
	r := originTimeLast(t, p, observer)
	if r.xid != xid {
		t.Fatal("actual write identity", r, xid)
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT (pg_xact_commit_timestamp_origin($1::xid)).timestamp,(pg_xact_commit_timestamp_origin($1::xid)).roident::bigint", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "INT8"}, [][]any{{fixture.Normalize(r.stamp), r.origin}}, xid)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, s)
	}
	return r
}
func TestReferenceReplicationOriginSessionCommitTimestamp(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE origin_time_rows(v int)", 0)
		id := originTimeRegistration(t, p, b, a, "session_time")
		fixture.LockExec(t, p.Ctx, a, "SELECT pg_replication_origin_session_reset()", 1)
		// Ordinary assigned completion populates the backend-global fallback.
		// Selection alone must preserve it, without xact setup or another reset.
		plainBefore := commitTimeClock(t, p, observer)
		plain := originTimeWrite(t, p, b, a, observer, -2)
		commitTimeBounded(t, plain, plainBefore, commitTimeClock(t, p, observer))
		if plain.origin != 0 {
			t.Fatal("ordinary write acquired an origin", plain)
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("SELECT pg_replication_origin_session_setup($1::text)", 1, "session_time"))
		selected := originTimeWrite(t, p, b, a, observer, -1)
		if selected.origin != id || !selected.stamp.Equal(plain.stamp) {
			t.Fatal("selection lost the preceding ordinary commit timestamp", plain, selected, id)
		}
		q := "SELECT pg_replication_origin_xact_setup($1::pg_lsn,$2::timestamptz)"
		stmt, err := a.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if _, err := stmt.ExecContext(p.Ctx, "0/10", "2022-03-04 05:06:07+00"); err != nil {
			t.Fatal(err)
		}
		stamp := time.Date(2022, 3, 4, 5, 6, 7, 0, time.UTC)
		for i := 1; i <= 2; i++ {
			r := originTimeWrite(t, p, b, a, observer, i)
			if r.origin != id || !r.stamp.Equal(stamp) {
				t.Fatal("supplied persistent origin", r, id, stamp)
			}
		}
		originTimeDropAbortWitness(t, p, b, a, observer, id)
		// Read-only completion creates no record. Assigned abort advances only progress.
		baseline := originTimeLast(t, p, observer)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		commitTimeSame(t, originTimeLast(t, p, observer), baseline)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "SELECT pg_replication_origin_xact_setup('0/20','2022-03-04+00')", 1)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO origin_time_rows VALUES(20)", 1)
		var aborted string
		if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&aborted); err != nil {
			t.Fatal(err)
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		commitTimeUnavailable(t, p, b, observer, aborted)
		commitTimeSame(t, originTimeLast(t, p, observer), baseline)
		for i, reset := range []string{"SELECT pg_replication_origin_xact_reset()", "SELECT pg_replication_origin_xact_setup('0/30','2000-01-01 00:00:00+00')"} {
			fixture.LockExec(t, p.Ctx, a, reset, 1)
			before := commitTimeClock(t, p, observer)
			first := originTimeWrite(t, p, b, a, observer, 30+i*2)
			after := commitTimeClock(t, p, observer)
			if first.origin != id {
				t.Fatal("fallback lost identity", first, id)
			}
			commitTimeBounded(t, first, before, after)
			next := originTimeWrite(t, p, b, a, observer, 31+i*2)
			if next.origin != id || !next.stamp.Equal(first.stamp) {
				t.Fatal("fallback was not retained in physical context", first, next)
			}
		}
		for i, stamp := range []string{"infinity", "-infinity"} {
			if _, err := stmt.ExecContext(p.Ctx, "0/40", stamp); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO origin_time_rows VALUES($1)", 1, 40+i))
			var xid string
			if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT (pg_xact_commit_timestamp_origin($1::xid)).timestamp::text AS stamp,(pg_xact_commit_timestamp_origin($1::xid)).roident::bigint AS origin", []string{"stamp", "origin"}, []string{"TEXT", "INT8"}, [][]any{{stamp, id}}, xid))
		}
		fixture.LockExec(t, p.Ctx, a, "SELECT pg_replication_origin_session_reset()", 1)
		before := commitTimeClock(t, p, observer)
		normal := originTimeWrite(t, p, b, a, observer, 50)
		commitTimeBounded(t, normal, before, commitTimeClock(t, p, observer))
		if normal.origin != 0 {
			t.Fatal(normal)
		}
	})
}
func TestReferenceReplicationOriginSessionPreparedCompletion(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, finisher *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE origin_prepared_rows(v int)", 0)
		originTimeRegistration(t, p, b, a, "session_preparing")
		fixture.LockExec(t, p.Ctx, a, "SELECT pg_replication_origin_xact_setup('0/80','2022-03-04 05:06:07+00')", 1)
		var originB int64
		for i, gid := range []string{"session_prepared_plain", "session_prepared_selected", "session_prepared_abort"} {
			baseline := originTimeLast(t, p, finisher)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO origin_prepared_rows VALUES($1)", 1, i))
			fixture.PreparedReferencePrepare(t, p, b, a, gid)
			entry := fixture.PreparedReferenceOne(t, p, finisher, gid)
			xid := strconv.FormatUint(uint64(entry.Xid), 10)
			commitTimeUnavailable(t, p, b, finisher, xid)
			commitTimeSame(t, originTimeLast(t, p, finisher), baseline)
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT pg_replication_origin_session_is_setup(),pg_replication_origin_session_progress(false)", []string{"pg_replication_origin_session_is_setup", "pg_replication_origin_session_progress"}, []string{"BOOL", "PG_LSN"}, [][]any{{true, "0/80"}}))
			fixture.RunStep(t, p.Ctx, finisher, b.Recorder, fixture.Select("SELECT count(*) AS v FROM origin_prepared_rows WHERE v=$1", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}, i))
			if i == 1 {
				originB = originTimeRegistration(t, p, b, finisher, "session_finishing")
				fixture.LockExec(t, p.Ctx, finisher, "SELECT pg_replication_origin_xact_setup('0/90','2023-04-05 06:07:08+00')", 1)
			}
			if i == 2 {
				fixture.LockExec(t, p.Ctx, finisher, "SELECT pg_replication_origin_xact_setup('0/A0','2024-05-06+00')", 1)
			}
			before := commitTimeClock(t, p, finisher)
			command := "COMMIT"
			if i == 2 {
				command = "ROLLBACK"
			}
			fixture.LockExec(t, p.Ctx, finisher, fmt.Sprintf("%s PREPARED '%s'", command, gid), 0)
			if i == 2 {
				commitTimeUnavailable(t, p, b, finisher, xid)
				commitTimeSame(t, originTimeLast(t, p, finisher), baseline)
			} else {
				r := originTimeLast(t, p, finisher)
				if r.xid != xid {
					t.Fatal(r, xid)
				}
				if i == 0 {
					if r.origin != 0 {
						t.Fatal("preparing backend context transferred", r)
					}
					commitTimeBounded(t, r, before, commitTimeClock(t, p, finisher))
				} else if r.origin != originB || !r.stamp.Equal(time.Date(2023, 4, 5, 6, 7, 8, 0, time.UTC)) {
					t.Fatal("finishing actor origin not used", r, originB)
				}
			}
			if i > 0 {
				position := []string{"", "0/90", "0/A0"}[i]
				fixture.RunStep(t, p.Ctx, finisher, b.Recorder, fixture.Select("SELECT pg_replication_origin_session_progress(false) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{position}}))
			}
		}
		fixture.RunStep(t, p.Ctx, finisher, b.Recorder, fixture.Select("SELECT v FROM origin_prepared_rows ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
	})
}

func originTimeDropAbortWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn, id int64) {
	t.Helper()
	q := "SELECT pg_replication_origin_drop($1::text)"
	stmt, err := a.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}
	pid := fixture.LockPID(t, p.Ctx, a)
	before := originTimeLast(t, p, observer)
	fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("SELECT pg_replication_origin_xact_setup('0/90','2022-03-04 05:06:07+00')", 1))
	for _, actor := range []fixture.Executor{a, held} {
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55006", Message: fmt.Sprintf("could not drop replication origin with ID %d, in use by PID %d", id, pid)}, "session_time"))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT pg_replication_origin_progress('session_time',false) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/10"}}))
		commitTimeSame(t, originTimeLast(t, p, observer), before)
	}
	retained := originTimeWrite(t, p, b, a, observer, 3)
	if retained.origin != id || !retained.stamp.Equal(before.stamp) {
		t.Fatal("active DROP erased the pending origin timestamp", retained, before)
	}
	for i, actor := range []fixture.Executor{a, held} {
		baseline := originTimeLast(t, p, observer)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("SELECT pg_replication_origin_xact_setup('0/90','2022-03-04 05:06:07+00')", 1))
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO origin_time_rows VALUES($1)", 1, 90+i))
		var xid string
		if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, actor, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "55006", Message: fmt.Sprintf("could not drop replication origin with ID %d, in use by PID %d", id, pid)}, "session_time"))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT pg_replication_origin_progress('session_time',false) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{"0/90"}}))
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT count(*) AS v FROM origin_time_rows WHERE v=$1", []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}, 90+i))
		commitTimeUnavailable(t, p, b, observer, xid)
		commitTimeSame(t, originTimeLast(t, p, observer), baseline)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	}
}
