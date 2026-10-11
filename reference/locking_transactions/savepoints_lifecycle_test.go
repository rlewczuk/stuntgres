package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsLifecycle(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		ctx := f.Pair.Ctx
		tx, err := f.A.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		f.Exec(t, tx, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, tx, "SAVEPOINT s", false, 0)
		f.Exec(t, tx, "INSERT INTO t VALUES(2)", false, 1)
		want := fixture.TransactionNull()
		fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(NULL)", true), &want)
		f.Exec(t, tx, "ROLLBACK TO s", false, 0)
		if err := tx.Commit(); err != nil {
			t.Fatal("qualified Go handle lost after recovery", err)
		}
		f.Rows(t, f.B, 1)
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "SAVEPOINT s", false, 0)
		f.Exec(t, f.A, "COMMIT AND CHAIN", false, 0)
		step := fixture.TransactionError(false, "ROLLBACK TO s", fixture.ControlsReferenceDiagnostic("3B001", `savepoint "s" does not exist`))
		fixture.RunStep(t, ctx, f.A, f.Recorder, step)
		f.Exec(t, f.A, "ROLLBACK", false, 0)
		f.NoticesWant(t, "", "")
	})
}
