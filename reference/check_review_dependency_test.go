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

func TestReferenceCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed=%t/prepared=%t", mixed, prepared), func(t *testing.T) {
				referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
					run(referenceExec("CREATE SEQUENCE s", 0))
					detail := "constraint c1 on table t depends on sequence s\nconstraint c2 on table t depends on sequence s"
					if mixed {
						run(referenceExec("CREATE TABLE t(a INT CONSTRAINT c1 CHECK(nextval('s')>0))", 0))
						run(referenceExec("CREATE VIEW v AS SELECT nextval('s') AS n", 0))
						detail = "constraint c1 on table t depends on sequence s\nview v depends on sequence s"
					} else {
						run(referenceExec("CREATE TABLE t(a INT,CONSTRAINT c1 CHECK(nextval('s')>0),CONSTRAINT c2 CHECK(nextval('s')>0))", 0))
					}
					run(referenceExec("INSERT INTO t VALUES(1)", 1))
					// REL_18_0 dependency.c reportDependentObjects supplies the
					// initial complete golden; the pinned run observes its order.
					s := referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
					s.prepared = prepared
					run(s)
					run(referenceExec("INSERT INTO t VALUES(2)", 1))
					run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					if mixed {
						run(referenceSelect("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
					}
				})
			})
		}
	}
}

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

func TestReferenceCheckReviewDependencyPrivateDeletionNonwaiting(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := newReferencePair(t)
			ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
			defer cancel()
			a, b := referenceLockConnections(t, ctx, p.postgres)
			referenceLockExec(t, ctx, a, "CREATE SEQUENCE s", 0)
			var providerOID int64
			if err := a.QueryRowContext(ctx, "SELECT 's'::regclass::oid").Scan(&providerOID); err != nil || providerOID <= 0 || providerOID > 1<<32-1 {
				t.Fatal("invalid sequence identity", providerOID, err)
			}
			tx, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceLockExec(t, ctx, tx, "DROP SEQUENCE s CASCADE", 0)
			const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
			done := make(chan relationOutcome, 1)
			go func() { r, e := b.ExecContext(ctx, create); done <- relationOutcome{result: r, err: e} }()
			// tests-6 pinned PostgreSQL observation: CREATE finishes successfully
			// while DROP remains private, under both eventual outcomes.
			var o relationOutcome
			select {
			case o = <-done:
			case <-ctx.Done():
				t.Fatal("CREATE did not complete before private DROP ended", ctx.Err())
			}
			if o.err != nil {
				t.Fatal(o.err)
			}
			referenceLockAffected(t, o.result, 0)
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			var providerPresent, tablePresent bool
			if err = b.QueryRowContext(ctx, "SELECT to_regclass('s') IS NOT NULL, to_regclass('newly_dependent') IS NOT NULL").Scan(&providerPresent, &tablePresent); err != nil {
				t.Fatal(err)
			}
			if providerPresent != !commit || !tablePresent {
				t.Fatal("unexpected final identities", providerPresent, tablePresent)
			}
			var checks int
			if err = b.QueryRowContext(ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='newly_dependent'::regclass AND contype='c'").Scan(&checks); err != nil || checks != 1 {
				t.Fatal("unexpected CHECK set", checks, err)
			}
			if commit {
				result, insertErr := b.ExecContext(ctx, "INSERT INTO newly_dependent VALUES(1)")
				if insertErr == nil || result != nil {
					t.Fatal("expected unusable deleted provider without successful INSERT", result, insertErr)
				}
				var rows int
				if err = b.QueryRowContext(ctx, "SELECT count(*) FROM newly_dependent").Scan(&rows); err != nil || rows != 0 {
					t.Fatal("failed INSERT changed rows", rows, err)
				}
				checkReviewDependencyPGDiagnostic(t, insertErr, referenceDiagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", providerOID)})
				return
			}
			referenceLockExec(t, ctx, b, "INSERT INTO newly_dependent VALUES(1)", 1)
			var aValue int
			if err = b.QueryRowContext(ctx, "SELECT a FROM newly_dependent").Scan(&aValue); err != nil || aValue != 1 {
				t.Fatal(aValue, err)
			}
			_, err = b.ExecContext(ctx, "DROP SEQUENCE s")
			checkReviewDependencyPGDiagnostic(t, err, referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c on table newly_dependent depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		})
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

func TestReferenceCheckReviewDependencyRestrictReversedDeclaration(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE SEQUENCE s", 0))
		run(referenceExec("CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))", 0))
		// Prospective ordering golden, disambiguated by the pinned oracle
		// from CHECK's separate alphabetical execution ordering.
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("DROP SEQUENCE s", referenceDiagnostic{Code: "2BP01", Message: "cannot drop sequence s because other objects depend on it", Detail: "constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
			s.prepared = prepared
			run(s)
		}
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
