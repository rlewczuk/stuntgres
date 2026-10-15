package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommitTimeControls(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_controls(v INT, stamp TIMESTAMPTZ DEFAULT pg_xact_commit_timestamp('1'::xid))", 0)
		fixture.LockExec(t, p.Ctx, a, "CREATE VIEW commit_time_controls_last AS SELECT * FROM pg_last_committed_xact()", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_controls(v) VALUES(7)", 1)
		baseline := commitTimeLast(t, p, observer)
		commitTimeLookup(t, p, a, baseline)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v, stamp FROM commit_time_controls", []string{"v", "stamp"}, []string{"INT4", "TIMESTAMPTZ"}, [][]any{{int64(7), nil}}))
		// Native controls have no PostgreSQL equivalent; the common SQL witness
		// above remains real on both backends, without inventing an oracle reset API.
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "pg_xact_commit_timestamp") || !strings.Contains(dump.String(), "pg_last_committed_xact") {
			t.Fatalf("dump lost symbolic inquiries: %s", dump.String())
		}
		for _, script := range []string{"CREATE TABLE broken(v INT); INSERT INTO broken VALUES('bad');", "BEGIN; COMMIT;"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
			commitTimeLookup(t, p, a, baseline)
		}
		readErr := errors.New("commit timestamp restore reader")
		if err := stuntgres.Restore(p.Ctx, p.Native, transactionControlFailingReader{readErr}); !errors.Is(err, readErr) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		for _, replacement := range []string{"restore", "reset"} {
			old := commitTimeLast(t, p, observer)
			if replacement == "restore" {
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
			} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SHOW track_commit_timestamp", []string{"track_commit_timestamp"}, []string{"TEXT"}, [][]any{{"on"}}))
			fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT pg_last_committed_xact()::text", []string{"pg_last_committed_xact"}, []string{"TEXT"}, [][]any{{"(,,)"}}))
			commitTimeUnavailable(t, p, b, observer, old.xid)
			if replacement == "reset" {
				fixture.LockExec(t, p.Ctx, a, "CREATE TABLE commit_time_controls(v INT, stamp TIMESTAMPTZ DEFAULT pg_xact_commit_timestamp('1'::xid))", 0)
			} else {
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT * FROM commit_time_controls_last", []string{"xid", "timestamp", "roident"}, []string{"XID", "TIMESTAMPTZ", "OID"}, [][]any{{nil, nil, nil}}))
			}
			fixture.LockExec(t, p.Ctx, a, "INSERT INTO commit_time_controls(v) VALUES(9)", 1)
			next := commitTimeLast(t, p, observer)
			oldID, _ := strconv.ParseUint(old.xid, 10, 32)
			nextID, _ := strconv.ParseUint(next.xid, 10, 32)
			if nextID <= oldID {
				t.Fatalf("replacement reused public identity: old=%s new=%s", old.xid, next.xid)
			}
			commitTimeLookup(t, p, a, next)
		}
	})
}
