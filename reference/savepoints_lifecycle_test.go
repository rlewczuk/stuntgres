package reference_test

import "testing"

func TestReferenceSavepointsLifecycle(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		ctx := f.pair.ctx
		tx, err := f.a.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		f.exec(t, tx, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, tx, "SAVEPOINT s", false, 0)
		f.exec(t, tx, "INSERT INTO t VALUES(2)", false, 1)
		want := referenceTransactionNull()
		referenceAssertDiagnostic(t, transactionConstraintExec(t, ctx, tx, "INSERT INTO t VALUES(NULL)", true), &want)
		f.exec(t, tx, "ROLLBACK TO s", false, 0)
		if err := tx.Commit(); err != nil {
			t.Fatal("qualified Go handle lost after recovery", err)
		}
		f.rows(t, f.b, 1)
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "SAVEPOINT s", false, 0)
		f.exec(t, f.a, "COMMIT AND CHAIN", false, 0)
		step := referenceTransactionError(false, "ROLLBACK TO s", controlsReferenceDiagnostic("3B001", `savepoint "s" does not exist`))
		referenceRunStep(t, ctx, f.a, f.recorder, step)
		f.exec(t, f.a, "ROLLBACK", false, 0)
		f.noticesWant(t, "", "")
	})
}
