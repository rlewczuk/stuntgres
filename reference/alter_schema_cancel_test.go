//go:build reference

package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceAlterSchemaCancellation(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1", "s2")
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	a, b := referenceLockConnections(t, ctx, p.postgres)
	r := &referenceRecorder{}
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
	referenceLockExec(t, ctx, holder, "LOCK TABLE s1.t IN ACCESS EXCLUSIVE MODE", 0)
	stmt, err := b.PrepareContext(ctx, "ALTER TABLE s1.t SET SCHEMA s2")
	if err != nil {
		t.Fatal("Prepare must not wait", err)
	}
	defer stmt.Close()
	done := make(chan relationOutcome, 1)
	go func() { result, err := stmt.ExecContext(ctx); done <- relationOutcome{result: result, err: err} }()
	schemaShadowWait(t, ctx, pid, "s1.t")
	var canceled bool
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
		t.Fatal(canceled, err)
	}
	out := referenceLockResult(t, done)
	want := controlsReferenceDiagnostic("57014", "canceling statement due to user request")
	referenceAssertDiagnostic(t, out.err, &want)
	if out.result != nil {
		t.Fatal("canceled move exposed result")
	}
	var pending int
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("waiter leak", pending, err)
	}
	referenceRunStep(t, ctx, b, r, referenceSelect("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='s1' ORDER BY c.relname", []string{"relname"}, []string{"NAME"}, [][]any{{"free"}, {"q1"}, {"q2"}, {"t"}}))
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, ctx, b, r, referenceAlterSchemaRows("SELECT id,note FROM s1.t ORDER BY id"))
	result, err := stmt.ExecContext(ctx)
	if err != nil {
		t.Fatal("canceled handle not reusable", err)
	}
	referenceLockAffected(t, result, 0)
	referenceRunStep(t, ctx, b, r, referenceAlterSchemaRows("SELECT id,note FROM s2.t ORDER BY id"))
}
