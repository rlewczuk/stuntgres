package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func combinationCases(t *testing.T, body func(*fixture.RoleMembershipScenario)) {
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
					s := &fixture.RoleMembershipScenario{T: t, P: p, Backend: b, C: c, Record: record, Notices: &notices, Prepared: prepared, Bootstrap: bootstrap}
					defer c.ExecContext(p.Ctx, "RESET ROLE")
					for _, name := range names {
						s.Exec("CREATE ROLE " + name)
					}
					s.Exec("GRANT USAGE ON SCHEMA public TO comb_reader,comb_group,comb_other,comb_middle")
					s.Exec("GRANT comb_group TO comb_reader WITH INHERIT TRUE, SET TRUE")
					s.Exec("CREATE TABLE t(id INT PRIMARY KEY,allowed BOOL,v INT)")
					s.Run(fixture.Exec("INSERT INTO t VALUES(1,TRUE,10),(2,FALSE,20),(3,NULL,30),(4,TRUE,40)", 4))
					s.Exec("GRANT SELECT,INSERT,UPDATE,DELETE ON t TO comb_reader,comb_group,comb_middle")
					s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
					body(s)
				})
			}
		})
	}
}
func combinationIDs(s *fixture.RoleMembershipScenario, q string, ids ...int64) {
	s.T.Helper()
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	s.Run(fixture.Select(q, []string{"id"}, []string{"INT4"}, rows))
}
func combinationDeny(s *fixture.RoleMembershipScenario, q, policy string, query bool) {
	s.T.Helper()
	message := `new row violates row-level security policy for table "t"`
	if policy != "" {
		message = `new row violates row-level security policy "` + policy + `" for table "t"`
	}
	predefinedFailure(s, q, fixture.Diagnostic{Code: "42501", Message: message}, query, "execute")
}
func combinationError(s *fixture.RoleMembershipScenario, q, code, message string, query bool) {
	s.T.Helper()
	predefinedFailure(s, q, fixture.Diagnostic{Code: code, Message: message}, query, "execute")
}
func combinationHeld(s *fixture.RoleMembershipScenario, q string) fixture.HeldStatement {
	s.T.Helper()
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	s.T.Cleanup(func() { stmt.Close() })
	return fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: q}
}
func combinationHeldIDs(s *fixture.RoleMembershipScenario, h fixture.HeldStatement, ids ...int64) {
	rows := make([][]any, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	fixture.RunStep(s.T, s.P.Ctx, h, s.Record, fixture.Select(h.SQL, []string{"id"}, []string{"INT4"}, rows))
}

func combinationViewDemandSource(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("CREATE TABLE helper(obsolete INT,id INT)")
	s.Exec("ALTER TABLE helper DROP COLUMN obsolete")
	s.Run(fixture.Exec("INSERT INTO helper(id) VALUES(1),(3)", 2))
	s.Exec("CREATE VIEW helper_view AS SELECT 10/(id-id) AS unused,id+0 AS lookup FROM helper")
	s.Exec("GRANT SELECT ON helper_view TO comb_reader")
}
