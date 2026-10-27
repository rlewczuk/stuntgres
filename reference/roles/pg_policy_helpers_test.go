package reference_test

import (
	"fmt"
	"strings"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

// All eight stored fields are checked. Generated identities are compared with
// independently resolved object addresses and principals, never allocator goldens.
func pgPolicyRow(table, name, command string, permissive bool, roles, using, check string) fixture.Step {
	refs := []string{}
	for _, role := range strings.Split(strings.Trim(roles, "{}"), ",") {
		if role == "public" {
			refs = append(refs, "0::oid")
		} else {
			refs = append(refs, "(SELECT oid FROM pg_roles WHERE rolname="+pq.QuoteLiteral(role)+")")
		}
	}
	q := "SELECT oid=(SELECT objid FROM pg_get_object_address('policy',ARRAY['public'," + pq.QuoteLiteral(table) + "," + pq.QuoteLiteral(name) + "],ARRAY[]::text[])) AS identity,polname,polrelid=" + pq.QuoteLiteral(table) + "::regclass AS relation,polcmd,polpermissive,polroles=ARRAY[" + strings.Join(refs, ",") + "]::oid[] AS roles,pg_get_expr(polqual,polrelid) AS qual,pg_get_expr(polwithcheck,polrelid) AS check_expr FROM pg_policy WHERE polrelid=" + pq.QuoteLiteral(table) + "::regclass AND polname=" + pq.QuoteLiteral(name)
	return fixture.Select(q, []string{"identity", "polname", "relation", "polcmd", "polpermissive", "roles", "qual", "check_expr"}, []string{"BOOL", "NAME", "BOOL", "CHAR", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{true, name, true, command, permissive, true, policyNullable(using), policyNullable(check)}})
}

func pgPolicyFacts(s *fixture.RoleMembershipScenario, table, name, command string, permissive bool, roles, using, check string) {
	s.T.Helper()
	s.Run(pgPolicyRow(table, name, command, permissive, roles, using, check))
}

func (s *transactionalPolicies) facts(table, name, command string, permissive bool, roles, using, check string) {
	s.t.Helper()
	step := pgPolicyRow(table, name, command, permissive, roles, using, check)
	step.Prepared = s.held
	s.run(step)
}

func pgPolicyAbsent(table, name string) fixture.Step {
	return fixture.Select("SELECT polname FROM pg_policy WHERE polrelid="+pq.QuoteLiteral(table)+"::regclass AND polname="+pq.QuoteLiteral(name), []string{"polname"}, []string{"NAME"}, [][]any{})
}

func pgPolicyExpressions(s *fixture.RoleMembershipScenario, table, name, normal, pretty string) {
	s.T.Helper()
	q := fmt.Sprintf("SELECT pg_get_expr(polqual,polrelid) AS implicit,pg_get_expr(polqual,polrelid,false) AS normal,pg_get_expr(polqual,polrelid,true) AS pretty,pg_get_expr(polwithcheck,polrelid) AS check_normal,pg_get_expr(polwithcheck,polrelid,true) AS check_pretty FROM pg_policy WHERE polrelid=%s::regclass AND polname=%s", pq.QuoteLiteral(table), pq.QuoteLiteral(name))
	s.Run(fixture.Select(q, []string{"implicit", "normal", "pretty", "check_normal", "check_pretty"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{normal, normal, pretty, normal, pretty}}))
}

// A peer must inspect pg_policy without acquiring a target-table lock behind
// the transaction owner's ALTER. Both identities were captured before BEGIN.
func pgPolicyKnownRow(step fixture.Step, table, name string, oid, relation int64) fixture.Step {
	address := "(SELECT objid FROM pg_get_object_address('policy',ARRAY['public'," + pq.QuoteLiteral(table) + "," + pq.QuoteLiteral(name) + "],ARRAY[]::text[]))"
	step.Query = strings.ReplaceAll(step.Query, address, fmt.Sprintf("%d::oid", oid))
	step.Query = strings.ReplaceAll(step.Query, pq.QuoteLiteral(table)+"::regclass", fmt.Sprintf("%d::oid", relation))
	return pgPolicyStructuralRow(step)
}

// Raw structural presence is portable; PostgreSQL's private serialization is
// not. This supplements exact display tests without comparing private bytes.
func pgPolicyStructuralRow(step fixture.Step) fixture.Step {
	step.Query = strings.ReplaceAll(step.Query, "pg_get_expr(polqual,polrelid)", "(CASE WHEN polqual IS NULL THEN NULL::boolean ELSE polqual::text<>'' END)")
	step.Query = strings.ReplaceAll(step.Query, "pg_get_expr(polwithcheck,polrelid)", "(CASE WHEN polwithcheck IS NULL THEN NULL::boolean ELSE polwithcheck::text<>'' END)")
	step.Types[6], step.Types[7] = "BOOL", "BOOL"
	for _, column := range []int{6, 7} {
		if step.Rows[0][column] != nil {
			step.Rows[0][column] = true
		}
	}
	return step
}
