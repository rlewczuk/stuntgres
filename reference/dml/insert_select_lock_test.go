//go:build reference

package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertSelectSourceLock(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE s(i INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE t(i INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO s VALUES(1)", 1))
	for _, backend := range []struct {
		db     *sql.DB
		record *fixture.Recorder
	}{{p.Memory, p.MemoryRecord}, {p.Postgres, p.PostgresRecord}} {
		waiter, err := backend.db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer waiter.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, waiter, backend.record, s) }
			run(fixture.Exec("SET lock_timeout='40ms'", 0))
			defer waiter.ExecContext(p.Ctx, "SET lock_timeout='0'")
			cached, err := waiter.PrepareContext(p.Ctx, "INSERT INTO t SELECT i FROM s")
			if err != nil {
				t.Fatal(err)
			}
			defer cached.Close()
			holder, err := backend.db.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.RunStep(t, p.Ctx, holder, backend.record, fixture.Exec("LOCK TABLE s IN ACCESS EXCLUSIVE MODE", 0))
			for _, prepared := range []bool{false, true} {
				failure := fixture.ImplicitError("INSERT INTO t(missing) SELECT i FROM s", fixture.Diagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"})
				failure.Prepared = prepared
				failure.ErrorStage = "execute"
				if prepared {
					failure.ErrorStage = "prepare"
				}
				run(failure)
			}
			// Holder acquisition is synchronous; every producer is blocked on source AS.
			for _, prepared := range []bool{false, true} {
				failure := fixture.ImplicitError("INSERT INTO t SELECT i FROM s", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "29"})
				failure.Prepared = prepared
				failure.ErrorStage = "execute"
				if prepared {
					failure.ErrorStage = "prepare"
				}
				run(failure)
			}
			result, err := cached.ExecContext(p.Ctx)
			if result != nil {
				t.Fatal("failed cached producer exposed result")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"})
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("INSERT INTO t SELECT i FROM s", 1))
			run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}()
	}
}
