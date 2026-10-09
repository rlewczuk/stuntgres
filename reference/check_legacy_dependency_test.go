//go:build reference

package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// Observe the actual pinned server wait independently of native parity. A
// prematurely completed command is a failure, not a successful wait witness.
func checkReviewDependencyPGWait(t *testing.T, ctx context.Context, pid int, done <-chan relationOutcome) {
	t.Helper()
	for {
		select {
		case o := <-done:
			t.Fatalf("command completed before provider-lock wait: result=%v error=%v", o.result, o.err)
		default:
		}
		var waiting bool
		if err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}

func checkReviewDependencyPGDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	got := referenceExtractDiagnostic(t, err)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("complete PostgreSQL diagnostic got %#v want %#v", got, want)
	}
}

func TestReferenceCheckReviewDependencyCreationWaitsDeletion(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprint(cascade), func(t *testing.T) {
			p := newReferencePair(t)
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			defer cancel()
			a, b := referenceLockConnections(t, ctx, p.postgres)
			referenceLockExec(t, ctx, a, "CREATE SEQUENCE s", 0)
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// PostgreSQL's transaction keeps CREATE's provider hold until
			// publication. Native CREATE transactions are not falsely implied.
			referenceLockExec(t, ctx, tx, "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))", 0)
			referenceLockExec(t, ctx, tx, "INSERT INTO newly_dependent VALUES(1)", 1)
			var pid int
			if err = b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			q := "DROP SEQUENCE s"
			if cascade {
				q += " CASCADE"
			}
			done := make(chan relationOutcome, 1)
			go func() { r, e := b.ExecContext(ctx, q); done <- relationOutcome{result: r, err: e} }()
			checkReviewDependencyPGWait(t, ctx, pid, done)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			o := referenceLockResult(t, done)
			if cascade {
				if o.err != nil {
					t.Fatal(o.err)
				}
				referenceLockAffected(t, o.result, 0)
			} else {
				if o.result != nil {
					t.Fatal("failed DROP exposed result")
				}
				checkReviewDependencyPGDiagnostic(t, o.err, referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table newly_dependent depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			}
			referenceLockExec(t, ctx, b, "INSERT INTO newly_dependent VALUES(2)", 1)
			var count, checks int
			if err = b.QueryRowContext(ctx, "SELECT count(*) FROM newly_dependent").Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			if err = b.QueryRowContext(ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='newly_dependent'::regclass AND contype='c'").Scan(&checks); err != nil {
				t.Fatal(err)
			}
			wantChecks := 1
			if cascade {
				wantChecks = 0
			}
			if checks != wantChecks {
				t.Fatal("invalid surviving CHECK set", checks, wantChecks)
			}
		})
	}
}
