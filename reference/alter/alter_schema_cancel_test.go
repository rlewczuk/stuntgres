//go:build reference

package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterSchemaCancellation(t *testing.T) {
	p := fixture.NewPair(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	a, b := fixture.LockConnections(t, ctx, p.Postgres)
	r := &fixture.Recorder{}
	referenceAlterSchemaSetup(t, p, a, r)
	var pid int
	if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	holder, err := a.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	fixture.LockExec(t, ctx, holder, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE", 0)
	stmt, err := b.PrepareContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2")
	if err != nil {
		t.Fatal("Prepare must not wait", err)
	}
	defer stmt.Close()
	done := make(chan fixture.RelationOutcome, 1)
	go func() {
		result, err := stmt.ExecContext(ctx)
		done <- fixture.RelationOutcome{Result: result, Err: err}
	}()
	fixture.SchemaShadowWait(t, ctx, pid, "s1.t")
	var canceled bool
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
		t.Fatal(canceled, err)
	}
	out := fixture.LockResult(t, done)
	want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
	fixture.AssertDiagnostic(t, out.Err, &want)
	if out.Result != nil {
		t.Fatal("canceled move exposed result")
	}
	var pending int
	if err := fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("waiter leak", pending, err)
	}
	fixture.RunStep(t, ctx, b, r, fixture.Select("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s1' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"free"}, {"q1"}, {"q2"}, {"t"}}))
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, ctx, b, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
	result, err := stmt.ExecContext(ctx)
	if err != nil {
		t.Fatal("canceled handle not reusable", err)
	}
	fixture.LockAffected(t, result, 0)
	fixture.RunStep(t, ctx, b, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
}
