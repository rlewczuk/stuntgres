package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
)

// Transport selection follows the existing COPY reference scenarios. Native
// controls require the unwrapped pool; the oracle uses its genuine pgx stream.
func commitTimeCopyFrom(ctx context.Context, c *sql.Conn, pg bool, source io.Reader) (stuntgres.CopyResult, error) {
	const query = "COPY commit_time_copy FROM STDIN"
	if !pg {
		return stuntgres.CopyFrom(ctx, c, query, source)
	}
	var result stuntgres.CopyResult
	err := c.Raw(func(raw any) error {
		conn, ok := raw.(*stdlib.Conn)
		if !ok {
			return fmt.Errorf("unexpected oracle COPY connection %T", raw)
		}
		tag, err := conn.Conn().PgConn().CopyFrom(ctx, source, query)
		if err == nil {
			result = stuntgres.CopyResult{Count: tag.RowsAffected(), CommandTag: tag.String()}
		}
		return err
	})
	return result, err
}

func TestReferenceCommitTimeCopyPublication(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, owner, "CREATE TABLE commit_time_copy(v INT)", 0)
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		copyBackend := b
		copyBackend.DB = db
		copyConn := fixture.ConcurrentIndexConn(t, p, copyBackend)
		baseline := commitTimeLast(t, p, observer)
		for i, explicit := range []bool{false, true} {
			if explicit {
				fixture.LockExec(t, p.Ctx, copyConn, "BEGIN", 0)
			}
			before := commitTimeClock(t, p, observer)
			input := []string{"1\n2\n", "3\n4\n"}[i]
			result, err := commitTimeCopyFrom(p.Ctx, copyConn, b.PG, strings.NewReader(input))
			if err != nil || result != (stuntgres.CopyResult{Count: 2, CommandTag: "COPY 2"}) {
				t.Fatalf("successful COPY result=%+v err=%v", result, err)
			}
			if explicit {
				commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_copy ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				before = commitTimeClock(t, p, observer)
				fixture.LockExec(t, p.Ctx, copyConn, "COMMIT", 0)
			}
			after := commitTimeClock(t, p, observer)
			next := commitTimeLast(t, p, observer)
			if next.xid == baseline.xid {
				t.Fatal("successful COPY did not record a new completion")
			}
			commitTimeBounded(t, next, before, after)
			commitTimeLookup(t, p, owner, next)
			baseline = next
		}
		result, err := commitTimeCopyFrom(p.Ctx, copyConn, b.PG, strings.NewReader(""))
		if err != nil || result != (stuntgres.CopyResult{Count: 0, CommandTag: "COPY 0"}) {
			t.Fatalf("empty COPY result=%+v err=%v", result, err)
		}
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		result, err = commitTimeCopyFrom(p.Ctx, copyConn, b.PG, strings.NewReader("5\nbad\n"))
		if result != (stuntgres.CopyResult{}) {
			t.Fatalf("failed COPY returned a success result: %+v", result)
		}
		want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: `COPY commit_time_copy, line 2, column v: "bad"`}
		fixture.AssertDiagnostic(t, err, &want)
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
		commitTimeLookup(t, p, owner, baseline)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_copy ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
		commitTimeSame(t, commitTimeLast(t, p, observer), baseline)
	})
}

func TestReferenceCommitTimeConcurrentIndexPublication(t *testing.T) {
	commitTimeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, owner, "CREATE TABLE commit_time_index(v INT)", 0)
		fixture.LockExec(t, p.Ctx, owner, "INSERT INTO commit_time_index VALUES(1),(2)", 2)
		baseline := commitTimeLast(t, p, observer)
		before := commitTimeClock(t, p, observer)
		fixture.LockExec(t, p.Ctx, owner, "CREATE INDEX CONCURRENTLY commit_time_index_ok ON commit_time_index(v)", 0)
		after := commitTimeClock(t, p, observer)
		next := commitTimeLast(t, p, observer)
		if next.xid == baseline.xid {
			t.Fatal("concurrent index completion did not record its catalog work")
		}
		commitTimeBounded(t, next, before, after)
		commitTimeLookup(t, p, owner, next)
		fixture.ConcurrentIndexWait(t, p, b, "commit_time_index_ok", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		commitTimeSame(t, commitTimeLast(t, p, observer), next)

		fixture.LockExec(t, p.Ctx, owner, "INSERT INTO commit_time_index VALUES(1)", 1)
		baseline = commitTimeLast(t, p, observer)
		builder := fixture.ConcurrentIndexConn(t, p, b)
		holderPID := fixture.ConcurrentIndexPID(t, p, b, owner)
		builderPID := fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, owner, "INSERT INTO commit_time_index VALUES(3)", 1)
		before = commitTimeClock(t, p, observer)
		worker := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY commit_time_index_fail ON commit_time_index(v)", false)
		// The existing granted writer freezes the first durable phase before
		// scanning duplicates. No sleep or guessed internal commit count is used.
		fixture.ConcurrentIndexWait(t, p, b, "commit_time_index_fail", stuntgres.IndexState{Live: true}, builderPID, holderPID, "waiting for writers before build")
		phaseAfter := commitTimeClock(t, p, observer)
		phase := commitTimeLast(t, p, observer)
		if phase.xid == baseline.xid {
			t.Fatal("observed durable index phase has no recorded completion")
		}
		commitTimeBounded(t, phase, before, phaseAfter)
		commitTimeLookup(t, p, observer, phase)
		fixture.LockExec(t, p.Ctx, owner, "ROLLBACK", 0)
		err := worker.Join(t, p)
		want := fixture.ConcurrentIndexUnique("commit_time_index", "commit_time_index_fail", "Key (v)=(1) is duplicated.", true)
		fixture.AssertDiagnostic(t, err, &want)
		fixture.ConcurrentIndexWait(t, p, b, "commit_time_index_fail", stuntgres.IndexState{Live: true}, 0, 0, "")
		// The aborted build cannot synthesize another completion or rewrite the
		// already durable phase's timestamp; that invalid index remains visible.
		commitTimeSame(t, commitTimeLast(t, p, observer), phase)
		commitTimeSame(t, commitTimeLast(t, p, builder), phase)
		commitTimeLookup(t, p, observer, phase)
		fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Select("SELECT v FROM commit_time_index ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}))
		commitTimeSame(t, commitTimeLast(t, p, observer), phase)
	})
}
