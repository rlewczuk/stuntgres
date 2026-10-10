package reference_test

import "testing"

func TestReferencePrimaryKeyTransactions(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		for _, null := range []bool{false, true} {
			run(referenceExec("BEGIN", 0))
			run(referenceExec("INSERT INTO t VALUES(2)", 1))
			run(referenceExec("SAVEPOINT child", 0))
			s := referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists."))
			if null {
				s = referenceErrorDiagnostic("INSERT INTO t VALUES(NULL)", primaryKeyReferenceNull("t", "a", "null"))
			}
			run(referencePrepared(s))
			run(referenceImplicitError("SELECT a FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(referenceExec("ROLLBACK TO child", 0))
			run(referenceSelect("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(referenceExec("INSERT INTO t VALUES(3)", 1))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		run(referenceExec("BEGIN", 0))
		run(referenceExec("SAVEPOINT before_drop", 0))
		run(referenceExec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
		run(referenceExec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
		run(referenceExec("INSERT INTO t VALUES(1),(NULL)", 2))
		run(referenceExec("ROLLBACK TO before_drop", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
		run(referenceExec("ROLLBACK TO before_drop", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL)", primaryKeyReferenceNull("t", "a", "null")))
		run(referenceExec("ROLLBACK TO before_drop", 0))
		run(referenceExec("COMMIT", 0))
		run(primaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
		run(referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(referenceExec("BEGIN READ ONLY", 0))
		run(referenceImplicitError("ALTER TABLE t DROP CONSTRAINT t_pkey", referenceDiagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
		run(referenceExec("ROLLBACK", 0))
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
	})
}

func TestReferencePrimaryKeyCreateTransactionBoundary(t *testing.T) {
	primaryKeyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, _ := referenceLockConnections(t, p.ctx, b.db)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
		run(referenceExec("BEGIN", 0))
		q := "CREATE TABLE explicit_t(a INT PRIMARY KEY)"
		if b.pg {
			run(referencePrepared(referenceExec(q, 0)))
		} else {
			s := referencePrepared(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
			s.errorStage = "execute"
			run(s)
		}
		run(referenceExec("ROLLBACK", 0))
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='explicit_t' OR relname='explicit_t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
	})
}
