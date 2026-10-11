package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type refreshQueuedOutcome struct {
	result sql.Result
	err    error
}

// Distinct worker frames identify each real pending request in native profiles.
func refreshQueuedFirst(ctx context.Context, conn *sql.Conn, query string, done chan<- refreshQueuedOutcome) {
	result, err := conn.ExecContext(ctx, query)
	done <- refreshQueuedOutcome{result, err}
}

func refreshQueuedSecond(ctx context.Context, stmt *sql.Stmt, done chan<- refreshQueuedOutcome) {
	result, err := stmt.ExecContext(ctx)
	done <- refreshQueuedOutcome{result, err}
}

func refreshQueuedNativeWait(t *testing.T, ctx context.Context, marker string, done <-chan refreshQueuedOutcome, joined *bool) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	for {
		select {
		case outcome := <-done:
			*joined = true
			t.Fatal("refresh completed before target queue observation", marker, outcome.err)
		case <-ctx.Done():
			t.Fatal("refresh queue observation deadline", marker, ctx.Err())
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			wait, target, worker := false, false, false
			for {
				frame, more := frames.Next()
				wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
				target = target || strings.HasSuffix(frame.Function, ".acquireMaterializedRefresh")
				worker = worker || strings.HasSuffix(frame.Function, "."+marker)
				if !more {
					break
				}
			}
			if wait && target && worker {
				return
			}
		}
		runtime.Gosched()
	}
}

func refreshQueuedJoin(t *testing.T, ctx context.Context, done <-chan refreshQueuedOutcome, joined *bool) {
	t.Helper()
	select {
	case outcome := <-done:
		*joined = true
		if outcome.err != nil || outcome.result == nil {
			t.Fatal("queued refresh result", outcome.result, outcome.err)
		}
		count, err := outcome.result.RowsAffected()
		if err != nil || count != 0 {
			t.Fatal("queued refresh affected count", count, err)
		}
	case <-ctx.Done():
		t.Fatal("queued refresh join deadline", ctx.Err())
	}
}

func TestReferenceMaterializedRefreshQueuedReplacement(t *testing.T) {
	for _, tc := range []struct{ name, first string }{
		{"data_data", "WITH DATA"},
		{"no_data_data", "WITH NO DATA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					holder, first := fixture.LockConnections(t, p.Ctx, b.DB)
					second, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer second.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, second, b.Recorder, s) }
					refreshSeed(run)
					run(fixture.Exec("INSERT INTO mv_base VALUES(3,'c')", 1))
					identitySQL := "SELECT c.oid,'refresh_mv'::regtype::oid AS rowtype_oid,w.oid FROM pg_class c JOIN pg_rewrite w ON w.ev_class=c.oid WHERE c.relname='refresh_mv'"
					var relation, rowtype, rule int64
					if err := second.QueryRowContext(p.Ctx, identitySQL).Scan(&relation, &rowtype, &rule); err != nil {
						t.Fatal(err)
					}
					if relation <= 0 || rowtype <= 0 || rule <= 0 || relation == rule {
						t.Fatal("invalid initial identity", relation, rowtype, rule)
					}
					identity := []any{relation, rowtype, rule}
					if b.PG {
						identity = []any{strconv.FormatInt(relation, 10), strconv.FormatInt(rowtype, 10), strconv.FormatInt(rule, 10)}
					}
					var owner string
					if err := second.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
						t.Fatal(err)
					}
					prepared, err := second.PrepareContext(p.Ctx, "REFRESH MATERIALIZED VIEW refresh_mv")
					if err != nil {
						t.Fatal(err)
					}
					defer prepared.Close()
					firstPID, secondPID, holderPID := 0, 0, 0
					if b.PG {
						firstPID = fixture.LockPID(t, p.Ctx, first)
						secondPID = fixture.LockPID(t, p.Ctx, second)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
					}
					tx := fixture.QueryLockBegin(t, p.Ctx, holder)
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, materializedViewBag("refresh_mv"))
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					firstDone, secondDone := make(chan refreshQueuedOutcome, 1), make(chan refreshQueuedOutcome, 1)
					startedFirst, startedSecond, joinedFirst, joinedSecond := false, false, false, false
					defer func() {
						cancel()
						tx.Rollback()
						cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						for _, w := range []struct {
							started, joined bool
							done            <-chan refreshQueuedOutcome
						}{{startedFirst, joinedFirst, firstDone}, {startedSecond, joinedSecond, secondDone}} {
							if w.started && !w.joined {
								select {
								case <-w.done:
								case <-cleanup.Done():
									t.Error("queued refresh cleanup failed to join", cleanup.Err())
								}
							}
						}
					}()
					startedFirst = true
					go refreshQueuedFirst(ctx, first, "REFRESH MATERIALIZED VIEW refresh_mv "+tc.first, firstDone)
					if b.PG {
						fixture.LockWaitBehind(t, ctx, firstPID, holderPID, relation, "AccessExclusiveLock")
					} else {
						refreshQueuedNativeWait(t, ctx, "refreshQueuedFirst", firstDone, &joinedFirst)
					}
					// Start only after the first request is genuinely queued. Its AX is
					// a soft blocker for the second in PG; native manager ordering is FIFO.
					startedSecond = true
					go refreshQueuedSecond(ctx, prepared, secondDone)
					if b.PG {
						fixture.LockWaitBehind(t, ctx, secondPID, firstPID, relation, "AccessExclusiveLock")
					} else {
						refreshQueuedNativeWait(t, ctx, "refreshQueuedSecond", secondDone, &joinedSecond)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					refreshQueuedJoin(t, ctx, firstDone, &joinedFirst)
					refreshQueuedJoin(t, ctx, secondDone, &joinedSecond)
					full := refreshBag("refresh_mv", [][]any{{int64(1), "a"}, {int64(1), "a"}, {int64(2), nil}, {int64(3), "c"}})
					run(full)
					run(fixture.Prepared(full))
					fixture.MaterializedViewPopulation(run, "refresh_mv", true)
					run(fixture.Select(identitySQL, []string{"oid", "rowtype_oid", "oid"}, []string{"OID", "OID", "OID"}, [][]any{identity}))
					run(fixture.Select("SELECT matviewowner,definition FROM pg_matviews WHERE matviewname='refresh_mv'", []string{"matviewowner", "definition"}, []string{"NAME", "TEXT"}, [][]any{{owner, " SELECT i,\n    s\n   FROM mv_base;"}}))
				})
			}
		})
	}
}
