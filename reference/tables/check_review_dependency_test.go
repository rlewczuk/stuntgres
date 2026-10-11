package reference_test

import (
	"bytes"
	"context"
	"errors"
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

func TestReferenceCheckReviewDependencyPrivateDeletionNonwaiting(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprint(commit), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					a, err := backend.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s fixture.Step) { fixture.CheckRun(t, ctx, b, backend.Recorder, s) }
					fixture.CheckRun(t, ctx, a, backend.Recorder, fixture.Exec("CREATE SEQUENCE s", 0))
					var oid int64
					if backend.PG {
						if err := a.QueryRowContext(ctx, "SELECT 's'::regclass::oid").Scan(&oid); err != nil || oid <= 0 || oid > 1<<32-1 {
							t.Fatal(oid, err)
						}
					}
					tx, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.CheckRun(t, ctx, tx, backend.Recorder, fixture.Exec("DROP SEQUENCE s CASCADE", 0))
					const create = "CREATE TABLE newly_dependent(a INT CONSTRAINT c CHECK(nextval('s')>0))"
					if backend.PG {
						// The bounded synchronous call must finish BEFORE ending DROP.
						run(fixture.Exec(create, 0))
					} else {
						blocked, stop := context.WithTimeout(context.WithValue(ctx, fixture.RecorderKey{}, backend.Recorder), 50*time.Millisecond)
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
					if backend.PG {
						var providerPresent, tablePresent bool
						if err := b.QueryRowContext(ctx, "SELECT to_regclass('s') IS NOT NULL,to_regclass('newly_dependent') IS NOT NULL").Scan(&providerPresent, &tablePresent); err != nil || providerPresent != !commit || !tablePresent {
							t.Fatal(providerPresent, tablePresent, err)
						}
						var checks int
						if err := b.QueryRowContext(ctx, "SELECT count(*) FROM pg_constraint WHERE conrelid='newly_dependent'::regclass AND contype='c'").Scan(&checks); err != nil || checks != 1 {
							t.Fatal(checks, err)
						}
						if commit {
							run(fixture.ImplicitError("INSERT INTO newly_dependent VALUES(1)", fixture.Diagnostic{Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", oid)}))
							run(fixture.Select("SELECT count(*) FROM newly_dependent", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							return
						}
					} else {
						if commit {
							r, err := b.ExecContext(ctx, create)
							if err == nil || r != nil {
								t.Fatal("missing provider accepted", r, err)
							}
							run(fixture.Exec("CREATE TABLE newly_dependent(a INT CHECK(a=1))", 0))
						} else {
							run(fixture.Exec(create, 0))
						}
					}
					run(fixture.Exec("INSERT INTO newly_dependent VALUES(1)", 1))
					run(fixture.Select("SELECT a FROM newly_dependent", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
					if backend.PG {
						run(fixture.ImplicitError("DROP SEQUENCE s", referenceCheckReview2OrderError("constraint c on table newly_dependent depends on sequence s")))
					} else {
						fixture.CheckDump(t, p)
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
