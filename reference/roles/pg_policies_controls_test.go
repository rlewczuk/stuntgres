package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgPoliciesRoundtrip(s *fixture.RoleMembershipScenario, rows ...[]any) {
	s.T.Helper()
	pgPoliciesRead(s, rows...)
	if s.Backend.PG {
		return // PostgreSQL has no native Dump/Reset/Restore API.
	}
	saved := pgPoliciesSnapshot(s)
	dump := policyDump(s)
	if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil { s.T.Fatal(err) }
	pgPoliciesRead(s)
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil { s.T.Fatal(err) }
	pgPoliciesRead(s, rows...)
	if again := policyDump(s); again != dump { s.T.Fatalf("unstable redump\nbefore=%s\nafter=%s", dump, again) }
	saved()
}

func TestReferencePGPoliciesControls(t *testing.T) {
	pgPoliciesCases(t, []string{"policies_control"}, func(s *fixture.RoleMembershipScenario) {
		s.Exec("CREATE TABLE t(id INT,allowed BOOL)")
		s.Exec("CREATE TABLE helper(id INT)")
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE)", 2))
		s.Run(fixture.Exec("INSERT INTO helper VALUES(1)", 1))
		s.Exec("CREATE POLICY p ON t TO policies_control USING(EXISTS(SELECT 1 FROM helper WHERE helper.id=t.id))")
		s.Exec("ALTER TABLE helper RENAME TO renamed_helper")
		s.Exec("ALTER TABLE renamed_helper RENAME COLUMN id TO helper_key")
		s.Exec("ALTER TABLE t RENAME COLUMN id TO target_key")
		s.Exec("CREATE TABLE helper(id INT)")
		row := pgPoliciesRow("t", "p", "PERMISSIVE", "{policies_control}", "ALL", "(EXISTS ( SELECT 1\n   FROM renamed_helper\n  WHERE (renamed_helper.helper_key = t.target_key)))", nil)
		pgPoliciesRoundtrip(s, row)
		s.Run(fixture.Select("SELECT target_key,allowed FROM t ORDER BY target_key", []string{"target_key", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
		s.Run(fixture.Select("SELECT helper_key FROM renamed_helper", []string{"helper_key"}, []string{"INT4"}, [][]any{{int64(1)}}))
		if s.Backend.PG { return }
		before := policyDump(s)
		saved := pgPoliciesSnapshot(s)
		bad := "CREATE TABLE replacement(id INT); INSERT INTO replacement VALUES(99); CREATE POLICY bad ON replacement USING(1);"
		err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(bad))
		fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of POLICY must be type boolean, not type integer"})
		pgPoliciesRead(s, row)
		s.Run(fixture.Select("SELECT target_key,allowed FROM t ORDER BY target_key", []string{"target_key", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
		s.Run(fixture.Select("SELECT helper_key FROM renamed_helper", []string{"helper_key"}, []string{"INT4"}, [][]any{{int64(1)}}))
		if after := policyDump(s); after != before { s.T.Fatalf("failed staged restore changed catalog\n%s\n%s", before, after) }
		saved()
	})
}
