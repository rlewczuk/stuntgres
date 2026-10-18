package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed=%t/prepared=%t", mixed, prepared), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
						run(fixture.Exec("CREATE SEQUENCE s", 0))
						detail := "constraint c1 on table t depends on sequence s\nconstraint c2 on table t depends on sequence s"
						if mixed {
							run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT c1 CHECK(nextval('s')>0))", 0))
							run(fixture.Exec("CREATE VIEW v AS SELECT nextval('s') AS n", 0))
							detail = "constraint c1 on table t depends on sequence s\nview v depends on sequence s"
						} else {
							run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT c1 CHECK(nextval('s')>0),CONSTRAINT c2 CHECK(nextval('s')>0))", 0))
						}
						run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
						var before []byte
						if !b.PG {
							before = fixture.CheckDump(t, p)
						}
						s := fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
						s.Prepared = prepared
						run(s)
						if !b.PG && !bytes.Equal(before, fixture.CheckDump(t, p)) {
							t.Fatal("RESTRICT changed catalog/data/provider state")
						}
						run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
						run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						if mixed {
							run(fixture.Select("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
						}
					})
				}
			})
		}
	}
}

// PostgreSQL 18.6 locks a provider while recording a dependency on it, so a
// definition referencing a sequence another transaction is dropping waits for
// that transaction (18.0 did not, leaving an orphaned dependency). After a
// rollback it succeeds; after a commit it fails 42704.
func TestReferenceCheckReviewDependencyPrivateDeletionWaits(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(p.Ctx, 10*time.Second)
					defer cancel()
					a, b := fixture.LockConnections(t, ctx, backend.DB)
					observer, _ := fixture.LockConnections(t, ctx, backend.DB)
					holder, waiter := checkReviewSessionID(t, ctx, backend, a), checkReviewSessionID(t, ctx, backend, b)
					run := func(s fixture.Step) { fixture.CheckRun(t, ctx, b, backend.Recorder, s) }
					fixture.CheckRun(t, ctx, a, backend.Recorder, fixture.Exec("CREATE SEQUENCE s", 0))
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.CheckRun(t, ctx, tx, backend.Recorder, fixture.Exec("DROP SEQUENCE s CASCADE", 0))
					const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
					done := fixture.RowAsync(t, ctx, b, create, false, false)
					checkReviewWaitBehind(t, ctx, observer, waiter, holder, done)
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					o := fixture.LockResult(t, done)
					if commit {
						if o.Result != nil {
							t.Fatal("failed CREATE exposed result")
						}
						fixture.WaitsDiagnostic(t, o.Err, "42704", "referenced relation was concurrently dropped")
						run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname IN ('s','newly_dependent')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						run(fixture.Exec("CREATE TABLE newly_dependent(a INT CHECK(a=1))", 0))
					} else if o.Err != nil {
						t.Fatal(o.Err)
					}
					run(fixture.Exec("INSERT INTO newly_dependent VALUES(1)", 1))
					run(fixture.Select("SELECT a FROM newly_dependent", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					if !commit {
						run(fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint c on table newly_dependent depends on sequence s")))
					}
				})
			}
		})
	}
}

func TestReferenceCheckReviewDependencyRestrictReversedDeclaration(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s", 0))
			run(fixture.Exec("CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s"))
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
