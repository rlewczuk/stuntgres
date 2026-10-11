package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSavepointsSyntaxAndNestedRecovery(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		f.Exec(t, f.A, `SAVEPOINT "S"`, true, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(2)", false, 1)
		f.Exec(t, f.A, "SAVEPOINT s", true, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		f.Exec(t, f.A, `ROLLBACK TO "S"`, true, 0)
		_, err := f.A.ExecContext(f.Pair.Ctx, "ROLLBACK TO s")
		want := fixture.ControlsReferenceDiagnostic("3B001", `savepoint "s" does not exist`)
		fixture.AssertDiagnostic(t, err, &want)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, message, position string }{{"SAVEPOINT", "syntax error at end of input", "10"}, {"RELEASE", "syntax error at end of input", "8"}, {"ROLLBACK TO", "syntax error at end of input", "12"}, {"ABORT TO s", `syntax error at or near "TO"`, "7"}} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+tc.q, func(t *testing.T) {
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = tc.position
					step := fixture.TransactionError(prepared, tc.q, want)
					step.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, step)
				})
			}
		}
		f.Exec(t, f.A, `ROLLBACK TO "S"`, true, 0)
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "SAVEPOINT child", true, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(4)", false, 1)
		f.Exec(t, f.A, "RELEASE child", true, 0)
		err = fixture.TransactionConstraintExec(t, f.Pair.Ctx, f.A, "INSERT INTO t VALUES(NULL)", true)
		want = fixture.TransactionNull()
		fixture.AssertDiagnostic(t, err, &want)
		f.Exec(t, f.A, `ROLLBACK TO "S"`, true, 0)
		f.Rows(t, f.A, 1)
		f.Exec(t, f.A, "INSERT INTO t VALUES(3)", false, 1)
		f.Exec(t, f.A, "COMMIT", false, 0)
		f.Rows(t, f.B, 1, 3)
	})
}

func TestReferenceSavepointsKeywordName(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f fixture.ControlsReferenceFixture) {
				for _, tc := range []struct{ q, tag string }{{"RELEASE SAVEPOINT", "RELEASE SAVEPOINT"}, {"ROLLBACK TO SAVEPOINT", "ROLLBACK TO SAVEPOINT"}} {
					fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.TransactionError(prepared, tc.q, fixture.ControlsReferenceDiagnostic("25P01", tc.tag+" can only be used in transaction blocks")))
				}
				exec := func(q string, n int64) { f.Exec(t, f.A, q, prepared, n) }
				exec("BEGIN", 0)
				exec("INSERT INTO t VALUES(1)", 1)
				exec("SAVEPOINT SAVEPOINT", 0)
				exec("INSERT INTO t VALUES(2)", 1)
				exec("ROLLBACK TO SAVEPOINT", 0)
				f.Rows(t, f.A, 1)
				exec("INSERT INTO t VALUES(3)", 1)
				exec("RELEASE SAVEPOINT", 0)
				f.Rows(t, f.B)
				exec("COMMIT", 0)
				f.Rows(t, f.B, 1, 3)
				exec("BEGIN", 0)
				exec("SAVEPOINT savepoint", 0)
				err := fixture.TransactionConstraintExec(t, f.Pair.Ctx, f.A, "INSERT INTO t VALUES(NULL)", true)
				want := fixture.TransactionNull()
				fixture.AssertDiagnostic(t, err, &want)
				step := fixture.TransactionError(prepared, "RELEASE SAVEPOINT", fixture.TransactionAborted())
				step.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, step)
				exec("ROLLBACK TO SAVEPOINT", 0)
				exec("INSERT INTO t VALUES(5)", 1)
				exec("RELEASE SAVEPOINT SAVEPOINT", 0)
				exec("COMMIT", 0)
				f.Rows(t, f.B, 1, 3, 5)
				exec("BEGIN", 0)
				exec("SAVEPOINT known", 0)
				for range 2 {
					step := fixture.TransactionError(prepared, "ROLLBACK TO SAVEPOINT", fixture.ControlsReferenceDiagnostic("3B001", `savepoint "savepoint" does not exist`))
					step.ErrorStage = "execute"
					fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, step)
				}
				exec("ROLLBACK TO known", 0)
				exec("ROLLBACK", 0)
				f.NoticesWant(t, "", "")
			})
		})
	}
}
