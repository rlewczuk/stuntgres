package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

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
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				s := fixture.Select(tc.q, tc.columns, tc.types, tc.rows)
				s.Bag = tc.bag
				referenceTiesBoth(t, p, b, s)
			})
		})
	}
}

func TestReferenceQueryTiesMetadata(t *testing.T) {
	referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		referenceTiesSetup(t, p, b)
		for _, tc := range []struct {
			suffix string
			rows   [][]any
		}{
			{"FETCH FIRST 2 ROWS WITH TIES", [][]any{{}, {}, {}}}, {"FETCH FIRST 0 ROWS WITH TIES", nil}, {"OFFSET 99 FETCH FIRST ROW WITH TIES", nil},
		} {
			referenceTiesBoth(t, p, b, fixture.Select("SELECT FROM ties_t ORDER BY k "+tc.suffix, []string{}, []string{}, tc.rows))
		}
		for _, suffix := range []string{"FETCH FIRST 0 ROWS WITH TIES", "OFFSET 99 FETCH FIRST ROW WITH TIES"} {
			referenceTiesBoth(t, p, b, fixture.Select("SELECT id AS ident FROM ties_t ORDER BY k "+suffix, []string{"ident"}, []string{"INT4"}, nil))
		}
	})
}

func TestReferenceQueryTiesInsert(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				run := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE ties_dst(id SMALLINT)", 0))
				run(fixture.Exec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 3))
				run(fixture.Select("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 2, 3)))
				run(fixture.Exec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 0 ROWS WITH TIES", 0))
				run(fixture.Exec("UPDATE ties_t SET id=40000 WHERE id=3", 1))
				s := fixture.ImplicitError("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 2, 3)))
				run(fixture.Exec("UPDATE ties_t SET id=3 WHERE id=40000", 1))
				run(fixture.Exec("UPDATE ties_t SET id=40000 WHERE id=4", 1))
				run(fixture.Exec("INSERT INTO ties_dst SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 3))
				run(fixture.Select("SELECT id FROM ties_dst ORDER BY id", []string{"id"}, []string{"INT2"}, referenceLimitIDs(1, 1, 2, 2, 3, 3)))
				own, err := b.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer own.Rollback()
				fixture.LockExec(t, p.Ctx, own, "INSERT INTO ties_t VALUES(5,2)", 1)
				fixture.RunStep(t, p.Ctx, own, b.Recorder, fixture.Exec("INSERT INTO ties_t SELECT id,k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 4))
				if err := own.Commit(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id FROM ties_t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 1, 2, 2, 3, 3, 5, 5, 40000)))
			})
		})
	}
}

func TestReferenceQueryTiesAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ties_reader"})
	referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE USER ties_reader", 0))
		run(fixture.Exec("CREATE TABLE ties_acl_t(id INT,k INT)", 0))
		run(fixture.Exec("INSERT INTO ties_acl_t VALUES(1,1),(2,2),(3,2),(4,3)", 4))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ties_reader", 0))
		}
		defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		q := "SELECT id FROM ties_acl_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES"
		stmt, err := conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
		run(fixture.Exec("SET SESSION AUTHORIZATION ties_reader", 0))
		for _, suffix := range []string{"FETCH FIRST 2 ROWS WITH TIES", "FETCH FIRST 0 ROWS WITH TIES", "OFFSET 99 FETCH FIRST ROW WITH TIES"} {
			for _, pred := range []string{"", " WHERE FALSE"} {
				s := fixture.ImplicitError("SELECT id FROM ties_acl_t"+pred+" ORDER BY k "+suffix, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ties_acl_t"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Prepared(s))
			}
		}
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table ties_acl_t"})
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		s = fixture.Select(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3))
		s.Bag = true
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
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
	referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		referenceTiesSetup(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE SEQUENCE ties_bounds_s START WITH 1", 0))
		stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT k FROM ties_t ORDER BY k FETCH FIRST (nextval('ties_bounds_s')+1) ROWS WITH TIES OFFSET nextval('ties_bounds_s')")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT is_called FROM ties_bounds_s", []string{"is_called"}, []string{"BOOL"}, [][]any{{false}}))
		rows, err := stmt.QueryContext(p.Ctx)
		fixture.ViewValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, "k", "INT4", 2, 2, 3)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT nextval('ties_bounds_s') AS n", []string{"n"}, []string{"INT8"}, referenceLimitIDs(3)))
	})
}
