package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceListPartitionAtomicity(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, q := range []string{
			"CREATE TABLE lp_atomic(k INT,payload TEXT) PARTITION BY LIST(k)",
			"CREATE TABLE lp_atomic_a PARTITION OF lp_atomic FOR VALUES IN(1)",
			"CREATE TABLE lp_atomic_b PARTITION OF lp_atomic FOR VALUES IN(2)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(referenceRangeMiss("INSERT INTO lp_atomic VALUES(1,'a'),(2,'b'),(7,'fail')", "lp_atomic", "k", "7"))
		run(fixture.Select("SELECT k,payload FROM lp_atomic", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
		run(fixture.Exec("INSERT INTO lp_atomic VALUES(1,'a'),(2,'b')", 2))
		run(fixture.Exec("CREATE TABLE lp_atomic_def PARTITION OF lp_atomic DEFAULT PARTITION BY LIST(payload)", 0))
		run(fixture.Exec("CREATE TABLE lp_atomic_deep PARTITION OF lp_atomic_def DEFAULT", 0))
		run(fixture.Exec("INSERT INTO lp_atomic VALUES(NULL,'n'),(7,'d')", 2))
		for _, value := range []string{"NULL", "7"} {
			q := "CREATE TABLE bad PARTITION OF lp_atomic FOR VALUES IN(" + value + ")"
			run(referenceRangeError(q, "23514", `updated partition constraint for default partition "lp_atomic_def" would be violated by some row`, fixture.Diagnostic{Schema: "public", Table: "lp_atomic_def"}))
			referenceListAbsent(run, "bad")
			run(fixture.Select("SELECT k,payload FROM lp_atomic ORDER BY k NULLS LAST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(7), "d"}, {nil, "n"}}))
			run(fixture.Select("SELECT k FROM lp_atomic_deep ORDER BY k NULLS LAST", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}}))
		}
		run(fixture.Exec("CREATE TABLE bad PARTITION OF lp_atomic FOR VALUES IN(8)", 0))
		run(fixture.Exec("INSERT INTO lp_atomic VALUES(8,'later')", 1))
	})
}

func TestReferenceListPartitionCancellation(t *testing.T) {
	referenceListScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE lp_wait(k INT) PARTITION BY LIST(k)", 0))
		run(fixture.Exec("CREATE TABLE lp_wait_a PARTITION OF lp_wait FOR VALUES IN(1)", 0))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{"CREATE TABLE lp_wait_b PARTITION OF lp_wait FOR VALUES IN(2)", "INSERT INTO lp_wait VALUES(1),(1)"} {
				result, err := b.DB.ExecContext(ctx, q)
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("precanceled result=%v err=%v", result, err)
				}
			}
			referenceListAbsent(run, "lp_wait_b")
			run(referenceRangeRows("SELECT k FROM lp_wait"))
			tx, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, referenceRangeError("LOCK TABLE lp_wait IN ACCESS EXCLUSIVE MODE", "0A000", "locking partition hierarchies is unsupported", fixture.Diagnostic{}))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		} else {
			waiter, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer waiter.Close()
			hold, err := b.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback()
			if _, err := hold.ExecContext(p.Ctx, "LOCK TABLE lp_wait IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err := waiter.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			type outcome struct {
				result sql.Result
				err    error
			}
			result := make(chan outcome, 1)
			joined := false
			defer func() {
				cancel()
				hold.Rollback()
				if !joined {
					select {
					case <-result:
					case <-time.After(5 * time.Second):
						t.Error("CREATE cancellation worker did not join")
					}
				}
			}()
			go func() {
				r, e := waiter.ExecContext(ctx, "CREATE TABLE lp_wait_b PARTITION OF lp_wait FOR VALUES IN(2)")
				result <- outcome{r, e}
			}()
			for {
				var admitted bool
				if err := fixture.Admin.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='lp_wait'::regclass AND mode='AccessExclusiveLock' AND NOT granted)", pid).Scan(&admitted); err != nil {
					t.Fatal(err)
				}
				if admitted {
					break
				}
				select {
				case got := <-result:
					joined = true
					t.Fatalf("CREATE completed before admission: %v", got.err)
				default:
				}
				runtime.Gosched()
			}
			var signaled bool
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
				t.Fatalf("cancel=%t err=%v", signaled, err)
			}
			select {
			case got := <-result:
				joined = true
				if got.result != nil {
					t.Fatal("canceled CREATE exposed result")
				}
				fixture.AssertDiagnostic(t, got.err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := hold.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
		referenceListAbsent(run, "lp_wait_b")
		run(fixture.Exec("CREATE TABLE lp_wait_b PARTITION OF lp_wait FOR VALUES IN(2)", 0))
		run(fixture.Exec("INSERT INTO lp_wait VALUES(1),(2)", 2))
		run(referenceRangeRows("SELECT k FROM lp_wait ORDER BY k", 1, 2))
	})
}
