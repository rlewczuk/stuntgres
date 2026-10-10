package reference_test

import "testing"

func TestReferenceUniqueTransactionalLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, _ := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
			run(referenceExec("CREATE TABLE fresh(k INT CONSTRAINT fresh_u UNIQUE)", 0))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("INSERT INTO fresh VALUES(1)", 1))
			run(referenceExec("SAVEPOINT m", 0))
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO fresh VALUES(1)", uniqueReferenceDuplicate("fresh", "fresh_u", "Key (k)=(1) already exists."))))
			run(referenceExec("ROLLBACK TO m", 0))
			run(referenceExec("COMMIT", 0))
			run(referenceSelect("SELECT k FROM fresh", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			var oid, index int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&index); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SAVEPOINT before_type", 0))
			run(referenceExec("ALTER TABLE fresh ALTER k TYPE BIGINT", 0))
			var rebuilt, rebuiltIndex int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&rebuilt); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&rebuiltIndex); err != nil {
				t.Fatal(err)
			}
			if rebuilt == oid || rebuiltIndex == index {
				t.Fatal("private TYPE did not rebuild identities")
			}
			run(referenceExec("ROLLBACK TO before_type", 0))
			var restored, restoredIndex int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&restored); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&restoredIndex); err != nil {
				t.Fatal(err)
			}
			if restored != oid || restoredIndex != index {
				t.Fatal("savepoint did not restore original identities")
			}
			run(referenceExec("SAVEPOINT before_drop", 0))
			run(referenceExec("ALTER TABLE fresh DROP CONSTRAINT fresh_u", 0))
			run(referenceExec("INSERT INTO fresh VALUES(1)", 1))
			run(referenceExec("ROLLBACK TO before_drop", 0))
			run(referenceErrorDiagnostic("INSERT INTO fresh VALUES(1)", uniqueReferenceDuplicate("fresh", "fresh_u", "Key (k)=(1) already exists.")))
			run(referenceExec("ROLLBACK TO before_drop", 0))
			run(referenceExec("COMMIT", 0))
			run(referenceExec("CREATE TABLE collision(k TEXT CONSTRAINT collision_u UNIQUE)", 0))
			run(referenceExec("INSERT INTO collision VALUES('01')", 1))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("INSERT INTO collision VALUES('1')", 1))
			run(referenceExec("SAVEPOINT before_collision", 0))
			want := uniqueReferenceDuplicate("collision", "collision_u", "Key (k)=(1) is duplicated.")
			want.Message = "could not create unique index \"collision_u\""
			run(referenceErrorDiagnostic("ALTER TABLE collision ALTER k TYPE INT USING k::INT", want))
			run(referenceExec("ROLLBACK TO before_collision", 0))
			run(referenceSelect("SELECT k FROM collision ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}, {"1"}}))
			run(referenceExec("DELETE FROM collision WHERE k='1'", 1))
			run(referenceExec("ALTER TABLE collision ALTER k TYPE INT USING k::INT", 0))
			run(referenceSelect("SELECT k FROM collision", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceExec("ROLLBACK", 0))
			run(referenceSelect("SELECT k FROM collision", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}}))
			run(referenceErrorDiagnostic("INSERT INTO collision VALUES('01')", uniqueReferenceDuplicate("collision", "collision_u", "Key (k)=(01) already exists.")))
		})
	}
}

func TestReferenceUniqueTemporaryLifecycle(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := referenceLockConnections(t, p.ctx, b.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
			run(referenceExec("INSERT INTO t VALUES(1)", 1))
			run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(referenceSelect("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='u'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (k)"}}))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES(1)", uniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
			run(referenceExec("CREATE TEMP TABLE t(k TEXT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0))
			run(referenceExec("INSERT INTO t VALUES('x'),(NULL)", 2))
			run(referenceSelect("SELECT k FROM t ORDER BY k NULLS LAST", []string{"k"}, []string{"TEXT"}, [][]any{{"x"}, {nil}}))
			referenceRunStep(t, p.ctx, peer, b.recorder, referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			want := uniqueReferenceDuplicate("t", "u", "Key (k)=(x) already exists.")
			// TEMP namespace names contain backend-local identities. Capture the actual
			// object's namespace; never guess PostgreSQL or native temp allocators.
			var namespace string
			run(referenceExec("SET search_path=pg_temp,public", 0))
			if err := a.QueryRowContext(p.ctx, "SELECT current_schema").Scan(&namespace); err != nil || namespace == "" {
				t.Fatal("actual temp namespace", namespace, err)
			}
			run(referenceExec("SET search_path=public", 0))
			want.Schema = namespace
			run(referencePrepared(referenceErrorDiagnostic("INSERT INTO t VALUES($1)", want, "x")))
			run(referenceExec("BEGIN", 0))
			run(referenceExec("SAVEPOINT m", 0))
			run(referenceExec("ALTER TABLE t DROP CONSTRAINT u", 0))
			run(referenceExec("INSERT INTO t VALUES('x')", 1))
			run(referenceExec("ROLLBACK TO m", 0))
			run(referenceExec("COMMIT", 0))
			run(referenceErrorDiagnostic("INSERT INTO t VALUES('x')", want))
			run(referenceExec("DROP TABLE t", 0))
			run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceUniqueCreateTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					a, _ := referenceLockConnections(t, p.ctx, b.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, b.recorder, s) }
					run(referenceExec("BEGIN", 0))
					query := "CREATE TABLE explicit_t(k INT CONSTRAINT explicit_u UNIQUE)"
					s := referenceExec(query, 0)
					if !b.pg {
						s = referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
						s.errorStage = "execute"
					}
					if prepared {
						s = referencePrepared(s)
					}
					run(s)
					if b.pg {
						run(referenceExec("INSERT INTO explicit_t VALUES(1)", 1))
					}
					run(referenceExec("ROLLBACK", 0))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='explicit_t' OR relname='explicit_u'", []string{"relname"}, []string{"NAME"}, nil))
				})
			}
		})
	}
}
