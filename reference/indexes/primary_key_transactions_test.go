package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePrimaryKeyTransactions(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(a INT PRIMARY KEY)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		for _, null := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO t VALUES(2)", 1))
			run(fixture.Exec("SAVEPOINT child", 0))
			s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists."))
			if null {
				s = fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", fixture.PrimaryKeyReferenceNull("t", "a", "null"))
			}
			run(fixture.Prepared(s))
			run(fixture.ImplicitError("SELECT a FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(fixture.Exec("ROLLBACK TO child", 0))
			run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Exec("INSERT INTO t VALUES(3)", 1))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_drop", 0))
		run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT t_pkey", 0))
		run(fixture.Exec("ALTER TABLE t ALTER a DROP NOT NULL", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1),(NULL)", 2))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL)", fixture.PrimaryKeyReferenceNull("t", "a", "null")))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.PrimaryKeyReferenceCatalog("t", [][]any{{"t_a_not_null", "n", "NOT NULL a", nil}, {"t_pkey", "p", "PRIMARY KEY (a)", nil}}))
		run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(fixture.ImplicitError("ALTER TABLE t DROP CONSTRAINT t_pkey", fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "t_pkey", "Key (a)=(1) already exists.")))
	})
}

func TestReferencePrimaryKeyCreateTransactionBoundary(t *testing.T) {
	fixture.PrimaryKeyReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("BEGIN", 0))
		q := "CREATE TABLE explicit_t(a INT PRIMARY KEY)"
		if b.PG {
			run(fixture.Prepared(fixture.Exec(q, 0)))
		} else {
			s := fixture.Prepared(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"}))
			s.ErrorStage = "execute"
			run(s)
		}
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='explicit_t' OR relname='explicit_t_pkey'", []string{"relname"}, []string{"NAME"}, nil))
	})
}
