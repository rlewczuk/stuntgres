//go:build reference

package reference_test

import "testing"

func TestReferenceSavepointsSyntaxAndNestedRecovery(t *testing.T) {
	savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		f.exec(t, f.a, `SAVEPOINT "S"`, true, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(2)", false, 1)
		f.exec(t, f.a, "SAVEPOINT s", true, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		f.exec(t, f.a, `ROLLBACK TO "S"`, true, 0)
		_, err := f.a.ExecContext(f.pair.ctx, "ROLLBACK TO s")
		want := controlsReferenceDiagnostic("3B001", `savepoint "s" does not exist`)
		referenceAssertDiagnostic(t, err, &want)
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ q, message, position string }{{"SAVEPOINT", "syntax error at end of input", "10"}, {"RELEASE", "syntax error at end of input", "8"}, {"ROLLBACK TO", "syntax error at end of input", "12"}, {"ABORT TO s", `syntax error at or near "TO"`, "7"}} {
				t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+tc.q, func(t *testing.T) {
					want := controlsReferenceDiagnostic("42601", tc.message)
					want.Position = tc.position
					step := referenceTransactionError(prepared, tc.q, want)
					step.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
					referenceRunStep(t, f.pair.ctx, f.a, f.recorder, step)
				})
			}
		}
		f.exec(t, f.a, `ROLLBACK TO "S"`, true, 0)
		f.rows(t, f.a, 1)
		f.exec(t, f.a, "SAVEPOINT child", true, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(4)", false, 1)
		f.exec(t, f.a, "RELEASE child", true, 0)
		err = transactionConstraintExec(t, f.pair.ctx, f.a, "INSERT INTO t VALUES(NULL)", true)
		want = referenceTransactionNull()
		referenceAssertDiagnostic(t, err, &want)
		f.exec(t, f.a, `ROLLBACK TO "S"`, true, 0)
		f.rows(t, f.a, 1)
		f.exec(t, f.a, "INSERT INTO t VALUES(3)", false, 1)
		f.exec(t, f.a, "COMMIT", false, 0)
		f.rows(t, f.b, 1, 3)
	})
}

func TestReferenceSavepointsKeywordName(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			savepointsReference(t, func(t *testing.T, f controlsReferenceFixture) {
				for _, tc := range []struct{ q, tag string }{{"RELEASE SAVEPOINT", "RELEASE SAVEPOINT"}, {"ROLLBACK TO SAVEPOINT", "ROLLBACK TO SAVEPOINT"}} {
					referenceRunStep(t, f.pair.ctx, f.a, f.recorder, referenceTransactionError(prepared, tc.q, controlsReferenceDiagnostic("25P01", tc.tag+" can only be used in transaction blocks")))
				}
				exec := func(q string, n int64) { f.exec(t, f.a, q, prepared, n) }
				exec("BEGIN", 0)
				exec("INSERT INTO t VALUES(1)", 1)
				exec("SAVEPOINT SAVEPOINT", 0)
				exec("INSERT INTO t VALUES(2)", 1)
				exec("ROLLBACK TO SAVEPOINT", 0)
				f.rows(t, f.a, 1)
				exec("INSERT INTO t VALUES(3)", 1)
				exec("RELEASE SAVEPOINT", 0)
				f.rows(t, f.b)
				exec("COMMIT", 0)
				f.rows(t, f.b, 1, 3)
				exec("BEGIN", 0)
				exec("SAVEPOINT savepoint", 0)
				err := transactionConstraintExec(t, f.pair.ctx, f.a, "INSERT INTO t VALUES(NULL)", true)
				want := referenceTransactionNull()
				referenceAssertDiagnostic(t, err, &want)
				step := referenceTransactionError(prepared, "RELEASE SAVEPOINT", referenceTransactionAborted())
				step.errorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				referenceRunStep(t, f.pair.ctx, f.a, f.recorder, step)
				exec("ROLLBACK TO SAVEPOINT", 0)
				exec("INSERT INTO t VALUES(5)", 1)
				exec("RELEASE SAVEPOINT SAVEPOINT", 0)
				exec("COMMIT", 0)
				f.rows(t, f.b, 1, 3, 5)
				exec("BEGIN", 0)
				exec("SAVEPOINT known", 0)
				for range 2 {
					step := referenceTransactionError(prepared, "ROLLBACK TO SAVEPOINT", controlsReferenceDiagnostic("3B001", `savepoint "savepoint" does not exist`))
					step.errorStage = "execute"
					referenceRunStep(t, f.pair.ctx, f.a, f.recorder, step)
				}
				exec("ROLLBACK TO known", 0)
				exec("ROLLBACK", 0)
				f.noticesWant(t, "", "")
			})
		})
	}
}
