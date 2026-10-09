package reference_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestReferenceCheckReviewDependencyRestrictComplete(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed=%t/prepared=%t", mixed, prepared), func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
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
						var before []byte
						if !b.pg {
							before = referenceCheckDump(t, p)
						}
						s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError(detail))
						s.prepared = prepared
						run(s)
						if !b.pg && !bytes.Equal(before, referenceCheckDump(t, p)) {
							t.Fatal("RESTRICT changed catalog/data/provider state")
						}
						run(referenceExec("INSERT INTO t VALUES(2)", 1))
						run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						if mixed {
							run(referenceSelect("SELECT n FROM v", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
						}
					})
				}
			})
		}
	}
}

func TestReferenceCheckReviewDependencyPrivateDeletionNonwaiting(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
					defer cancel()
					a, err := backend.db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s referenceStep) { referenceCheckRun(t, ctx, b, backend.recorder, s) }
					referenceCheckRun(t, ctx, a, backend.recorder, referenceExec("CREATE SEQUENCE s", 0))
					var oid int64
					if backend.pg {
						if err := a.QueryRowContext(ctx, "SELECT 's'::regclass::oid").Scan(&oid); err != nil || oid <= 0 || oid > 1<<32-1 {
							t.Fatal(oid, err)
						}
					}
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					referenceCheckRun(t, ctx, tx, backend.recorder, referenceExec("DROP SEQUENCE s CASCADE", 0))
					const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
					if backend.pg {
						// The bounded synchronous call must finish BEFORE ending DROP.
						run(referenceExec(create, 0))
					} else {
						blocked, stop := context.WithTimeout(context.WithValue(ctx, referenceRecorderKey{}, backend.recorder), 50*time.Millisecond)
						r, err := b.ExecContext(blocked, create)
						stop()
						if r != nil || !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("private DROP must block native CREATE", r, err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("fabricated cancellation state", err)
						}
					}
					if commit {
						err = tx.Commit()
					} else {
						err = tx.Rollback()
					}
					if err != nil {
						t.Fatal(err)
					}
					if backend.pg {
						var providerPresent, tablePresent bool
						if err := b.QueryRowContext(ctx, "SELECT to_regclass('s') IS NOT NULL,to_regclass('newly_dependent') IS NOT NULL").Scan(&providerPresent, &tablePresent); err != nil || providerPresent != !commit || !tablePresent {
							t.Fatal(providerPresent, tablePresent, err)
						}
						var checks int
						if err := b.QueryRowContext(ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='newly_dependent'::regclass AND contype='c'").Scan(&checks); err != nil || checks != 1 {
							t.Fatal(checks, err)
						}
						if commit {
							run(referenceImplicitError("INSERT INTO newly_dependent VALUES(1)", referenceDiagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", oid)}))
							run(referenceSelect("SELECT count(*) FROM newly_dependent", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							return
						}
					} else {
						if commit {
							r, err := b.ExecContext(ctx, create)
							if err == nil || r != nil {
								t.Fatal("missing provider accepted", r, err)
							}
							run(referenceExec("CREATE TABLE newly_dependent(a INT CHECK(a=1))", 0))
						} else {
							run(referenceExec(create, 0))
						}
					}
					run(referenceExec("INSERT INTO newly_dependent VALUES(1)", 1))
					run(referenceSelect("SELECT a FROM newly_dependent", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					if backend.pg {
						run(referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint c on table newly_dependent depends on sequence s")))
					} else {
						referenceCheckDump(t, p)
					}
				})
			}
		})
	}
}

func TestReferenceCheckReviewDependencyRestrictReversedDeclaration(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE SEQUENCE s", 0))
			run(referenceExec("CREATE TABLE t(a INT,CONSTRAINT c2 CHECK(nextval('s')>0),CONSTRAINT c1 CHECK(nextval('s')>0))", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint c2 on table t depends on sequence s\nconstraint c1 on table t depends on sequence s"))
				s.prepared = prepared
				run(s)
			}
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
