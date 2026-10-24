package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPoliciesSavepoints(t *testing.T) {
	transactionalPolicyMatrix(t, []string{"sql", "go"}, func(s *transactionalPolicies, mode string) {
		s.exec("CREATE POLICY p ON t TO tp_reader USING(id<=2) WITH CHECK(allowed)")
		oid := s.oid("t", "p")
		insert, err := s.c.PrepareContext(s.p.Ctx, "INSERT INTO t VALUES($1,$2)")
		if err != nil {
			s.t.Fatal(err)
		}
		defer insert.Close()
		for _, commit := range []bool{false, true} {
			finish := s.begin(mode)
			s.ddl("ALTER POLICY p ON t USING(id=1)")
			s.run(fixture.Exec("INSERT INTO t VALUES(4,TRUE)", 1))
			s.exec("SAVEPOINT policy_mark")
			s.ddl("ALTER POLICY p ON t RENAME TO renamed")
			s.ddl("ALTER POLICY renamed ON t TO tp_other USING(id=2) WITH CHECK(FALSE)")
			s.run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
			s.ids()
			s.ddl("DROP POLICY renamed ON t")
			s.ids()
			s.exec("SET ROLE tp_reader")
			s.run(fixture.Exec("UPDATE t SET allowed=TRUE", 0))
			s.run(fixture.Exec("DELETE FROM t", 0))
			s.failure("INSERT INTO t VALUES(5,TRUE)", "42501", `new row violates row-level security policy for table "t"`)
			s.exec("ROLLBACK TO policy_mark")
			s.exec("RESET ROLE")
			s.sameOID("t", "p", oid)
			s.ids(1)
			transactionalPolicyRestoredCheck(s, insert)
			s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}))
			// Repeated rollback retains the mark and its exact definition/data.
			s.ddl("ALTER POLICY p ON t USING(FALSE)")
			s.exec("ROLLBACK TO policy_mark")
			s.sameOID("t", "p", oid)
			s.ids(1)
			transactionalPolicyRestoredCheck(s, insert)
			s.exec("RELEASE policy_mark")
			finish(commit)
			s.sameOID("t", "p", oid)
			if commit {
				s.ids(1)
				s.run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}))
			} else {
				s.ids(1, 2)
				s.run(fixture.Select("SELECT count(*) FROM t", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
			// Probe the final definition in a new transaction without changing
			// the exact committed row state used by the next iteration.
			probeEnd := s.begin(mode)
			transactionalPolicyRestoredCheck(s, insert)
			probeEnd(false)
		}
	})
}

// The held INSERT predates every policy mutation. Each proposed row gets its
// own mark so both successful writes and failed-block state are discarded.
func transactionalPolicyRestoredCheck(s *transactionalPolicies, insert *sql.Stmt) {
	s.t.Helper()
	const q = "INSERT INTO t VALUES($1,$2)"
	var executor fixture.Executor = s.x
	if s.held {
		h := insert
		if tx, ok := s.x.(*sql.Tx); ok {
			h = tx.StmtContext(s.p.Ctx, insert)
			defer h.Close()
		}
		executor = fixture.HeldStatement{Executor: s.x, Statement: h, SQL: q}
	}
	s.exec("SET ROLE tp_reader")
	for _, allowed := range []any{true, false, nil} {
		s.exec("SAVEPOINT check_probe")
		step := fixture.Exec(q, 1, int64(50), allowed)
		if allowed != true {
			step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`}, int64(50), allowed)
		}
		fixture.RunStep(s.t, s.p.Ctx, executor, s.b.Recorder, step)
		s.exec("ROLLBACK TO check_probe")
		s.exec("RELEASE check_probe")
	}
	s.exec("RESET ROLE")
}
