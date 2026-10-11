//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTypeCancellation(t *testing.T) {
	p := fixture.NewPair(t)
	for _, prepared := range []bool{false, true} {
		a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE typecancel(c INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO typecancel VALUES(7)", 1)
		q := "ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1"
		var stmt *sql.Stmt
		var err error
		if prepared {
			stmt, err = b.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
		}
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.LockExec(t, p.Ctx, holder, "LOCK TABLE typecancel IN ACCESS EXCLUSIVE MODE", 0)
		var pid int
		if err = b.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		done := make(chan fixture.RelationOutcome, 1)
		go func() {
			o := fixture.RelationOutcome{}
			if prepared {
				o.Result, o.Err = stmt.ExecContext(p.Ctx)
			} else {
				o.Result, o.Err = b.ExecContext(p.Ctx, q)
			}
			done <- o
		}()
		fixture.LockWait(t, p.Ctx, pid)
		var canceled bool
		if err = fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
			t.Fatal(canceled, err)
		}
		outcome := fixture.LockResult(t, done)
		fixture.AssertDiagnostic(t, outcome.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		if stmt != nil {
			stmt.Close()
		}
		if err = holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b, p.PostgresRecord, fixture.Select("SELECT c FROM typecancel", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.LockExec(t, p.Ctx, b, q, 0)
		fixture.RunStep(t, p.Ctx, b, p.PostgresRecord, fixture.Select("SELECT c FROM typecancel", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
		fixture.LockExec(t, p.Ctx, b, "DROP TABLE typecancel", 0)
		a.Close()
		b.Close()
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("CREATE TABLE typecancel(c INT)", 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("INSERT INTO typecancel VALUES(7)", 1))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if _, err := p.Memory.ExecContext(ctx, "ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT c FROM typecancel", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Exec("ALTER TABLE typecancel ALTER c TYPE BIGINT USING c+1", 0))
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT c FROM typecancel", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}}))
}
