package reference_test

import "testing"

func TestReferenceQueryTiesOrderCache(t *testing.T) {
	for _, tc := range []struct {
		q              string
		rows           [][]any
		columns, types []string
		bag            bool
	}{
		{"SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", referenceLimitIDs(1, 2, 3), []string{"id"}, []string{"INT4"}, true},
		{"SELECT id FROM ties_t ORDER BY k%2 FETCH FIRST ROW WITH TIES", referenceLimitIDs(2, 3), []string{"id"}, []string{"INT4"}, true},
		{"SELECT k AS key FROM ties_t ORDER BY key FETCH FIRST 2 ROWS WITH TIES", referenceLimitIDs(1, 2, 2), []string{"key"}, []string{"INT4"}, false},
		{"SELECT k FROM ties_t ORDER BY 1 FETCH FIRST 2 ROWS WITH TIES", referenceLimitIDs(1, 2, 2), []string{"k"}, []string{"INT4"}, false},
		{"SELECT DISTINCT k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", referenceLimitIDs(1, 2), []string{"k"}, []string{"INT4"}, false},
		{"SELECT count(*) AS n FROM ties_t GROUP BY k ORDER BY n FETCH FIRST ROW WITH TIES", referenceLimitIDs(1, 1), []string{"n"}, []string{"INT8"}, false},
		{"VALUES(1),(2),(2),(3) ORDER BY 1 FETCH FIRST 2 ROWS WITH TIES", referenceLimitIDs(1, 2, 2), []string{"column1"}, []string{"INT4"}, false},
		{"SELECT k FROM ties_t UNION ALL SELECT k FROM ties_t ORDER BY k FETCH FIRST 3 ROWS WITH TIES", referenceLimitIDs(1, 1, 2, 2, 2, 2), []string{"k"}, []string{"INT4"}, false},
	} {
		t.Run(tc.q, func(t *testing.T) {
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				s := referenceSelect(tc.q, tc.columns, tc.types, tc.rows)
				s.bag = tc.bag
				referenceTiesBoth(t, p, b, s)
			})
		})
	}
}

func TestReferenceQueryTiesMetadata(t *testing.T) {
	referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
		referenceTiesSetup(t, p, b)
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{"FETCH FIRST 2 ROWS WITH TIES", [][]any{{}, {}, {}}}, {"FETCH FIRST 0 ROWS WITH TIES", nil}, {"OFFSET 99 FETCH FIRST ROW WITH TIES", nil},
		} {
			referenceTiesBoth(t, p, b, referenceSelect("SELECT FROM ties_t ORDER BY k "+tc.suffix, []string{}, []string{}, tc.rows))
		}
		for _, suffix := range []string{"FETCH FIRST 0 ROWS WITH TIES", "OFFSET 99 FETCH FIRST ROW WITH TIES"} {
			referenceTiesBoth(t, p, b, referenceSelect("SELECT id AS ident FROM ties_t ORDER BY k "+suffix, []string{"ident"}, []string{"INT4"}, nil))
		}
	})
}

func TestReferenceQueryTiesInsert(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
				referenceTiesSetup(t, p, b)
				run := func(s referenceStep) {
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
				run(referenceExec("CREATE TABLE ties_dst(id SMALLINT)", 0))
				run(referenceExec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 3))
				run(referenceSelect("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 2, 3)))
				run(referenceExec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 0 ROWS WITH TIES", 0))
				run(referenceExec("UPDATE ties_t SET id=40000 WHERE id=3", 1))
				s := referenceImplicitError("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
				s.errorStage = "execute"
				run(s)
				run(referenceSelect("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 2, 3)))
				run(referenceExec("UPDATE ties_t SET id=3 WHERE id=40000", 1))
				run(referenceExec("UPDATE ties_t SET id=40000 WHERE id=4", 1))
				run(referenceExec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 3))
				run(referenceSelect("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 1, 2, 2, 3, 3)))
				own, err := b.db.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer own.Rollback()
				referenceLockExec(t, p.ctx, own, "INSERT INTO ties_t VALUES(5,2)", 1)
				referenceRunStep(t, p.ctx, own, b.recorder, referenceExec("INSERT INTO ties_t SELECT id,k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 4))
				if err := own.Commit(); err != nil {
					t.Fatal(err)
				}
				run(referenceSelect("SELECT id FROM ties_t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 1, 2, 2, 3, 3, 5, 5, 40000)))
			})
		})
	}
}

func TestReferenceQueryTiesAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"ties_reader"})
	referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE USER ties_reader", 0))
		run(referenceExec("CREATE TABLE ties_acl_t(id INT,k INT)", 0))
		run(referenceExec("INSERT INTO ties_acl_t VALUES(1,1),(2,2),(3,2),(4,3)", 4))
		if b.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO ties_reader", 0))
		}
		defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT id FROM ties_acl_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES"
		stmt, err := conn.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
		run(referenceExec("SET SESSION AUTHORIZATION ties_reader", 0))
		for _, suffix := range []string{"FETCH FIRST 2 ROWS WITH TIES", "FETCH FIRST 0 ROWS WITH TIES", "OFFSET 99 FETCH FIRST ROW WITH TIES"} {
			for _, pred := range []string{"", " WHERE FALSE"} {
				s := referenceImplicitError("SELECT id FROM ties_acl_t"+pred+" ORDER BY k "+suffix, referenceDiagnostic{Code: "42501", Message: "permission denied for table ties_acl_t"})
				s.selectRows = true
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
			}
		}
		s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table ties_acl_t"})
		s.selectRows = true
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		s = referenceSelect(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3))
		s.bag = true
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	})
}

func TestReferenceQueryTiesFeatureSmoke(t *testing.T) {
	// This smoke executes actual peer behavior, complete-key/NULL boundaries,
	// held inputs, lookahead effects, metadata, denial and atomic INSERT.
	t.Run("basics", TestReferenceQueryTiesBasics)
	t.Run("exists", TestReferenceQueryTiesExists)
	t.Run("replacement", TestReferenceQueryTiesReplacement)
	t.Run("nullable complete keys", TestReferenceQueryTiesNullableKeys)
	t.Run("peers", TestReferenceQueryTiesPeers)
	t.Run("prepared", TestReferenceQueryTiesPrepared)
	t.Run("bounds", TestReferenceQueryTiesBounds)
	t.Run("demand", TestReferenceQueryTiesDemand)
	t.Run("metadata", TestReferenceQueryTiesMetadata)
	t.Run("diagnostics", TestReferenceQueryTiesOracleQuestions)
	t.Run("authorization", TestReferenceQueryTiesAuthorization)
	t.Run("insert", TestReferenceQueryTiesInsert)
}

func TestReferenceQueryTiesBounds(t *testing.T) {
	referenceTiesScenario(t, func(p *referencePair, b referenceBackend) {
		referenceTiesSetup(t, p, b)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE SEQUENCE ties_bounds_s START WITH 1", 0))
		stmt, err := b.db.PrepareContext(p.ctx, "SELECT k FROM ties_t ORDER BY k FETCH FIRST (nextval('ties_bounds_s')+1) ROWS WITH TIES OFFSET nextval('ties_bounds_s')")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT is_called FROM ties_bounds_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		rows, err := stmt.QueryContext(p.ctx)
		referenceViewValues(t, relationOutcome{rows: rows, err: err}, "k", "INT4", 2, 2, 3)
		referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT nextval('ties_bounds_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(3)))
	})
}
