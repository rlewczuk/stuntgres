package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func serializableReferenceScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func serializableReferenceWant(detail string) fixture.Diagnostic {
	want := fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to read/write dependencies among transactions")
	want.Detail = "Reason code: " + detail
	want.Hint = "The transaction might succeed if retried."
	return want
}

// These schedules permit failure at registration or at final validation, but
// never permit a different primary, hint, optional fields or arbitrary Detail.
func serializableReferenceFailure(t *testing.T, err error) {
	t.Helper()
	got := fixture.ExtractDiagnostic(t, err)
	valid := false
	for _, site := range []string{
		"Canceled on identification as a pivot, during conflict out checking.",
		"Canceled on identification as a pivot, during conflict in checking.",
		"Canceled on identification as a pivot, during write.",
		"Canceled on identification as a pivot, during commit attempt.",
		"Canceled on commit attempt with conflict in from prepared pivot.",
	} {
		if got.Detail == "Reason code: "+site {
			valid = true
		}
	}
	if !valid {
		t.Fatalf("unexpected dependency failure site: %#v", got)
	}
	want := serializableReferenceWant(strings.TrimPrefix(got.Detail, "Reason code: "))
	fixture.AssertDiagnostic(t, err, &want)
	fixture.State(t, err, "40001")
}

func serializableReferenceRun(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
}

func serializableReferenceHeld(t *testing.T, ctx context.Context, x fixture.Executor, q string, held bool) fixture.Executor {
	t.Helper()
	if !held {
		return x
	}
	stmt, err := x.PrepareContext(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: x, Statement: stmt, SQL: q}
}

func serializableReferenceBegin(t *testing.T, ctx context.Context, c *sql.Conn, mode string, goTx bool) (fixture.Executor, func() error) {
	t.Helper()
	if goTx {
		isolation := sql.LevelSerializable
		if mode == "REPEATABLE READ" {
			isolation = sql.LevelRepeatableRead
		}
		tx, err := c.BeginTx(ctx, &sql.TxOptions{Isolation: isolation})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx, tx.Commit
	}
	fixture.LockExec(t, ctx, c, "BEGIN ISOLATION LEVEL "+mode, 0)
	return c, func() error { _, err := c.ExecContext(ctx, "COMMIT"); return err }
}

func serializableReferenceWrite(t *testing.T, ctx context.Context, x fixture.Executor, q string, held bool, count int64) error {
	t.Helper()
	var result sql.Result
	var err error
	if held {
		stmt, prepareErr := x.PrepareContext(ctx, q)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		result, err = stmt.ExecContext(ctx)
	} else {
		result, err = x.ExecContext(ctx, q)
	}
	if err != nil {
		if result != nil {
			t.Fatal("failed write returned a result")
		}
		serializableReferenceFailure(t, err)
		return err
	}
	fixture.LockAffected(t, result, count)
	return nil
}

func serializableReferenceRollback(ctx context.Context, x fixture.Executor) {
	if tx, ok := x.(interface{ Rollback() error }); ok {
		tx.Rollback()
		return
	}
	x.ExecContext(ctx, "ROLLBACK")
}
