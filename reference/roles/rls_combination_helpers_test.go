package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func combinationCases(t *testing.T, body func(*membershipScenario)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			names := []string{"comb_reader", "comb_group", "comb_other", "comb_middle"}
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					bootstrap := "postgres"
					if b.PG {
						bootstrap = "stuntgres"
					}
					s := &membershipScenario{t, p, b, c, record, &notices, prepared, bootstrap}
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					for _, name := range names {
						s.exec("CREATE ROLE " + name)
					}
					s.exec("GRANT USAGE ON SCHEMA public TO comb_reader,comb_group,comb_other,comb_middle")
					s.exec("GRANT comb_group TO comb_reader WITH INHERIT TRUE, SET TRUE")
					s.exec("CREATE TABLE t(id INT PRIMARY KEY,allowed BOOL,v INT)")
					s.run(fixture.Exec("INSERT INTO t VALUES(1,TRUE,10),(2,FALSE,20),(3,NULL,30),(4,TRUE,40)", 4))
					s.exec("GRANT SELECT,INSERT,UPDATE,DELETE ON t TO comb_reader,comb_group,comb_middle")
					s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
					body(s)
				})
			}
		})
	}
}
func combinationIDs(s *membershipScenario, q string, ids ...int64) {
	s.t.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	s.run(fixture.Select(q, []string{"id"}, []string{"INT4"}, rows))
}
func combinationDeny(s *membershipScenario, q, policy string, query bool) {
	s.t.Helper()
	message := `new row violates row-level security policy for table "t"`
	if policy != "" {
		message = `new row violates row-level security policy "` + policy + `" for table "t"`
	}
	predefinedFailure(s, q, fixture.Diagnostic{Code: "42501", Message: message}, query, "execute")
}
func combinationError(s *membershipScenario, q, code, message string, query bool) {
	s.t.Helper()
	predefinedFailure(s, q, fixture.Diagnostic{Code: code, Message: message}, query, "execute")
}
func combinationHeld(s *membershipScenario, q string) fixture.HeldStatement {
	s.t.Helper()
	stmt, err := s.c.PrepareContext(s.p.Ctx, q)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: q}
}
func combinationHeldIDs(s *membershipScenario, h fixture.HeldStatement, ids ...int64) {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	fixture.RunStep(s.t, s.p.Ctx, h, s.record, fixture.Select(h.SQL, []string{"id"}, []string{"INT4"}, rows))
}

func combinationViewDemandSource(s *membershipScenario) {
	s.t.Helper()
	s.exec("CREATE TABLE helper(obsolete INT,id INT)")
	s.exec("ALTER TABLE helper DROP COLUMN obsolete")
	s.run(fixture.Exec("INSERT INTO helper(id) VALUES(1),(3)", 2))
	s.exec("CREATE VIEW helper_view AS SELECT 10/(id-id) AS unused,id+0 AS lookup FROM helper")
	s.exec("GRANT SELECT ON helper_view TO comb_reader")
}
