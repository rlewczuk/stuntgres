package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func commitTimeScenario(t *testing.T, run func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, observer := fixture.LockConnections(t, p.Ctx, b.DB)
			run(t, p, b, a, observer)
		})
	}
}

type commitTimeRecord struct {
	xid    string
	stamp  time.Time
	origin int64
}

func commitTimeLast(t *testing.T, p *fixture.Pair, c *sql.Conn) commitTimeRecord {
	t.Helper()
	var r commitTimeRecord
	if err := c.QueryRowContext(p.Ctx, "SELECT * FROM pg_last_committed_xact()").Scan(&r.xid, &r.stamp, &r.origin); err != nil {
		t.Fatal(err)
	}
	xid, err := strconv.ParseUint(r.xid, 10, 32)
	if err != nil || xid < 3 || r.origin != 0 || r.stamp.IsZero() {
		t.Fatalf("invalid local commit: %+v (%v)", r, err)
	}
	return r
}

func commitTimeSame(t *testing.T, got, want commitTimeRecord) {
	t.Helper()
	if got.xid != want.xid || !got.stamp.Equal(want.stamp) || got.origin != want.origin {
		t.Fatalf("last commit: %+v want %+v", got, want)
	}
}

func commitTimeClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
	t.Helper()
	var stamp time.Time
	if err := c.QueryRowContext(p.Ctx, "SELECT clock_timestamp()").Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func commitTimeLookup(t *testing.T, p *fixture.Pair, c *sql.Conn, r commitTimeRecord) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		q := "SELECT pg_xact_commit_timestamp($1), (pg_xact_commit_timestamp_origin($1)).timestamp, (pg_xact_commit_timestamp_origin($1)).roident"
		var scalar, stamp time.Time
		var origin int64
		if prepared {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			err = stmt.QueryRowContext(p.Ctx, r.xid).Scan(&scalar, &stamp, &origin)
			stmt.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else if err := c.QueryRowContext(p.Ctx, q, r.xid).Scan(&scalar, &stamp, &origin); err != nil {
			t.Fatal(err)
		}
		if !scalar.Equal(r.stamp) || !stamp.Equal(r.stamp) || origin != 0 {
			t.Fatalf("lookup %v %v %d want %+v", scalar, stamp, origin, r)
		}
	}
}

func commitTimeUnavailable(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, xid string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.Select("SELECT pg_xact_commit_timestamp($1), pg_xact_commit_timestamp_origin($1)::text", []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin"}, []string{"TIMESTAMPTZ", "TEXT"}, [][]any{{nil, "(,)"}}, xid)
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func commitTimeBounded(t *testing.T, r commitTimeRecord, before, after time.Time) {
	t.Helper()
	if r.stamp.Before(before) || r.stamp.After(after) {
		t.Fatalf("commit %v outside server interval [%v,%v]", r.stamp, before, after)
	}
}

func TestReferenceCommitTimeLifecycle(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_lifecycle(v INT UNIQUE)", 0)
		for _, mode := range []string{"sql", "tx", "autocommit"} {
			t.Run(mode, func(t *testing.T) {
				before := commitTimeClock(t, p, observer)
				switch mode {
				case "sql":
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_lifecycle VALUES(1)", 1)
					// Bound the commit itself, not transaction/statement start.
					before = commitTimeClock(t, p, observer)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
				case "tx":
					tx, err := a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if _, err := tx.ExecContext(p.Ctx, "INSERT INTO commit_time_lifecycle VALUES(2)"); err != nil {
						t.Fatal(err)
					}
					before = commitTimeClock(t, p, observer)
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				case "autocommit":
					fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_lifecycle VALUES(3)", 1)
				}
				after := commitTimeClock(t, p, observer)
				r := commitTimeLast(t, p, observer)
				commitTimeBounded(t, r, before, after)
				commitTimeLookup(t, p, a, r)
				commitTimeSame(t, commitTimeLast(t, p, a), r)
			})
		}
		r := commitTimeLast(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "BEGIN READ ONLY", 0)
		commitTimeSame(t, commitTimeLast(t, p, a), r)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_lifecycle VALUES(4)", 1)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		commitTimeSame(t, commitTimeLast(t, p, observer), r)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		_, err := a.ExecContext(p.Ctx, "INSERT INTO commit_time_lifecycle VALUES(1)")
		fixture.State(t, err, "23505")
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		commitTimeSame(t, commitTimeLast(t, p, observer), r)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_lifecycle ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		commitTimeUnavailable(t, p, b, observer, "4294967295")
		commitTimeUnavailable(t, p, b, observer, "-1")
	})
}

func TestReferenceCommitTimePrepared(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_prepared(v INT)", 0)
		baseline := commitTimeLast(t, p, observer)
		ids := make([]string, 0, 3)
		for i, gid := range []string{"commit_time_older", "commit_time_newer", "commit_time_abort"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_prepared VALUES("+strconv.Itoa(i)+")", 1)
			fixture.PreparedReferencePrepare(t, p, b, a, gid)
			entry := fixture.PreparedReferenceOne(t, p, observer, gid)
			ids = append(ids, strconv.FormatUint(uint64(entry.Xid), 10))
			commitTimeUnavailable(t, p, b, observer, ids[i])
			commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		}
		older, _ := strconv.ParseUint(ids[0], 10, 32)
		newer, _ := strconv.ParseUint(ids[1], 10, 32)
		if older < 3 || newer <= older {
			t.Fatalf("allocation order %v", ids)
		}
		for _, i := range []int{1, 0} {
			gid := []string{"commit_time_older", "commit_time_newer"}[i]
			before := commitTimeClock(t, p, observer)
			fixture.LockExec(t, p.Ctx, observer, "COMMIT PREPARED '"+gid+"'", 0)
			after := commitTimeClock(t, p, observer)
			r := commitTimeLast(t, p, observer)
			if r.xid != ids[i] {
				t.Fatalf("prepared xid %s, committed %+v", ids[i], r)
			}
			commitTimeBounded(t, r, before, after)
			commitTimeLookup(t, p, a, r)
			commitTimeSame(t, commitTimeLast(t, p, a), r)
		}
		baseline = commitTimeLast(t, p, observer)
		fixture.LockExec(t, p.Ctx, observer, "ROLLBACK PREPARED 'commit_time_abort'", 0)
		commitTimeUnavailable(t, p, b, observer, ids[2])
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		// An empty PREPARE still assigns a real public XID, but not a timestamp.
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, a, "commit_time_empty")
		entry := fixture.PreparedReferenceOne(t, p, observer, "commit_time_empty")
		xid := strconv.FormatUint(uint64(entry.Xid), 10)
		commitTimeUnavailable(t, p, b, observer, xid)
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		held, err := observer.PrepareContext(p.Ctx, "SELECT pg_xact_commit_timestamp($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var pending sql.NullTime
		if err := held.QueryRowContext(p.Ctx, xid).Scan(&pending); err != nil || pending.Valid {
			t.Fatalf("prepared transaction already timestamped: %v %v", pending, err)
		}
		fixture.LockExec(t, p.Ctx, observer, "COMMIT PREPARED 'commit_time_empty'", 0)
		r := commitTimeLast(t, p, observer)
		if r.xid != xid {
			t.Fatalf("empty prepared xid %s, committed %+v", xid, r)
		}
		commitTimeLookup(t, p, a, r)
		if err := held.QueryRowContext(p.Ctx, xid).Scan(&pending); err != nil || !pending.Valid || !pending.Time.Equal(r.stamp) {
			t.Fatalf("held lookup did not observe commit: %v %v", pending, err)
		}
	})
}

func TestReferenceCommitTimeFeatureSmoke(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SHOW track_commit_timestamp", []string{"track_commit_timestamp"}, []string{"TEXT"}, [][]any{{"on"}}))
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_smoke(v INT)", 0)
		before := commitTimeClock(t, p, observer)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_smoke VALUES(7)", 1)
		after := commitTimeClock(t, p, observer)
		r := commitTimeLast(t, p, observer)
		commitTimeBounded(t, r, before, after)
		commitTimeLookup(t, p, a, r)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_smoke VALUES(9)", 1)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		commitTimeSame(t, commitTimeLast(t, p, observer), r)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_smoke", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_smoke VALUES(11)", 1)
		fixture.PreparedReferencePrepare(t, p, b, a, "commit_time_smoke")
		entry := fixture.PreparedReferenceOne(t, p, observer, "commit_time_smoke")
		fixture.LockExec(t, p.Ctx, observer, "COMMIT PREPARED 'commit_time_smoke'", 0)
		r = commitTimeLast(t, p, observer)
		if r.xid != strconv.FormatUint(entry.Xid, 10) {
			t.Fatalf("prepared identity %d, last %+v", entry.Xid, r)
		}
		commitTimeLookup(t, p, a, r)
	})
}
