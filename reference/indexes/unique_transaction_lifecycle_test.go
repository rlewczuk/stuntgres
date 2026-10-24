package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueTransactionalLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE fresh(k INT CONSTRAINT fresh_u UNIQUE)", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO fresh VALUES(1)", 1))
			run(fixture.Exec("SAVEPOINT m", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO fresh VALUES(1)", fixture.UniqueReferenceDuplicate("fresh", "fresh_u", "Key (k)=(1) already exists."))))
			run(fixture.Exec("ROLLBACK TO m", 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Select("SELECT k FROM fresh", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			var oid, index int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&index); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT before_type", 0))
			run(fixture.Exec("ALTER TABLE fresh ALTER k TYPE BIGINT", 0))
			var rebuilt, rebuiltIndex int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&rebuilt); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&rebuiltIndex); err != nil {
				t.Fatal(err)
			}
			if rebuilt == oid || rebuiltIndex == index {
				t.Fatal("private TYPE did not rebuild identities")
			}
			run(fixture.Exec("ROLLBACK TO before_type", 0))
			var restored, restoredIndex int64
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_constraint WHERE conname='fresh_u'").Scan(&restored); err != nil {
				t.Fatal(err)
			}
			if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='fresh_u'").Scan(&restoredIndex); err != nil {
				t.Fatal(err)
			}
			if restored != oid || restoredIndex != index {
				t.Fatal("savepoint did not restore original identities")
			}
			run(fixture.Exec("SAVEPOINT before_drop", 0))
			run(fixture.Exec("ALTER TABLE fresh DROP CONSTRAINT fresh_u", 0))
			run(fixture.Exec("INSERT INTO fresh VALUES(1)", 1))
			run(fixture.Exec("ROLLBACK TO before_drop", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO fresh VALUES(1)", fixture.UniqueReferenceDuplicate("fresh", "fresh_u", "Key (k)=(1) already exists.")))
			run(fixture.Exec("ROLLBACK TO before_drop", 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.Exec("CREATE TABLE collision(k TEXT CONSTRAINT collision_u UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO collision VALUES('01')", 1))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("INSERT INTO collision VALUES('1')", 1))
			run(fixture.Exec("SAVEPOINT before_collision", 0))
			want := fixture.UniqueReferenceDuplicate("collision", "collision_u", "Key (k)=(1) is duplicated.")
			want.Message = "could not create unique index \"collision_u\""
			run(fixture.ErrorDiagnostic("ALTER TABLE collision ALTER k TYPE INT USING k::INT", want))
			run(fixture.Exec("ROLLBACK TO before_collision", 0))
			run(fixture.Select("SELECT k FROM collision ORDER BY k", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}, {"1"}}))
			run(fixture.Exec("DELETE FROM collision WHERE k='1'", 1))
			run(fixture.Exec("ALTER TABLE collision ALTER k TYPE INT USING k::INT", 0))
			run(fixture.Select("SELECT k FROM collision", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT k FROM collision", []string{"k"}, []string{"TEXT"}, [][]any{{"01"}}))
			run(fixture.ErrorDiagnostic("INSERT INTO collision VALUES('01')", fixture.UniqueReferenceDuplicate("collision", "collision_u", "Key (k)=(01) already exists.")))
		})
	}
}

func TestReferenceUniqueTemporaryLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k INT CONSTRAINT u UNIQUE)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='u'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (k)"}}))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1)", fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(1) already exists.")))
			run(fixture.Exec("CREATE TEMP TABLE t(k TEXT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES('x'),(NULL)", 2))
			run(fixture.Select("SELECT k FROM t ORDER BY k NULLS LAST", []string{"k"}, []string{"TEXT"}, [][]any{{"x"}, {nil}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			want := fixture.UniqueReferenceDuplicate("t", "u", "Key (k)=(x) already exists.")
			// TEMP namespace names contain backend-local identities. Capture the actual
			// object's namespace; never guess PostgreSQL or native temp allocators.
			var namespace string
			run(fixture.Exec("SET search_path=pg_temp,public", 0))
			if err := a.QueryRowContext(p.Ctx, "SELECT current_schema").Scan(&namespace); err != nil || namespace == "" {
				t.Fatal("actual temp namespace", namespace, err)
			}
			run(fixture.Exec("SET search_path=public", 0))
			want.Schema = namespace
			run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO t VALUES($1)", want, "x")))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT m", 0))
			run(fixture.Exec("ALTER TABLE t DROP CONSTRAINT u", 0))
			run(fixture.Exec("INSERT INTO t VALUES('x')", 1))
			run(fixture.Exec("ROLLBACK TO m", 0))
			run(fixture.Exec("COMMIT", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES('x')", want))
			run(fixture.Exec("DROP TABLE t", 0))
			run(fixture.Select("SELECT k FROM t", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceUniqueCreateTransactionBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					run(fixture.Exec("BEGIN", 0))
					query := "CREATE TABLE explicit_t(k INT CONSTRAINT explicit_u UNIQUE)"
					s := fixture.Exec(query, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Exec("INSERT INTO explicit_t VALUES(1)", 1))
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT relname FROM pg_class WHERE relname='explicit_t' OR relname='explicit_u'", []string{"relname"}, []string{"NAME"}, nil))
				})
			}
		})
	}
}
