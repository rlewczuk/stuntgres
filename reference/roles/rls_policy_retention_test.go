package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func policyRoleBlocked(s *membershipScenario, role, policy string) {
	s.t.Helper()
	policyError(s, "DROP ROLE "+role, "2BP01", fmt.Sprintf(`role "%s" cannot be dropped because some objects depend on it`, role), "target of policy "+policy+" on table t", 0)
}

func policyColumnBlocked(s *membershipScenario, column, policy string) {
	s.t.Helper()
	policyError(s, "ALTER TABLE t ALTER COLUMN "+column+" TYPE TEXT", "0A000", "cannot alter type of a column used in a policy definition", fmt.Sprintf(`policy %s on table t depends on column "%s"`, policy, column), 0)
}

// The PG catalog is an additional independent oracle witness. Native definitions
// are observed through real Dump rather than a new test-only metadata API.
func policyFacts(s *membershipScenario, name, mode, command, role, using, check string) {
	s.t.Helper()
	if s.backend.PG {
		s.run(fixture.Select("SELECT permissive,cmd,roles::text,qual,with_check FROM pg_policies WHERE schemaname='public' AND tablename='t' AND policyname='"+name+"'", []string{"permissive", "cmd", "roles", "qual", "with_check"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{mode, command, "{" + role + "}", policyNullable(using), policyNullable(check)}}))
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	text := dump.String()
	if !strings.Contains(text, "CREATE POLICY ") || !strings.Contains(text, name) {
		s.t.Fatal("policy omitted from Dump", text)
	}
	policyExportFields(s, text, name, role, using, check)
	// Dependency probes below certify bound fields; the export certifies command
	// and permissiveness independently of future row-security enforcement.
	if mode != "PERMISSIVE" && !strings.Contains(text, "AS "+mode) || command != "ALL" && !strings.Contains(text, "FOR "+command) {
		s.t.Fatal("policy mode/command lost", text)
	}
}

func policyNullable(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func TestReferenceRLSPolicyAlterRetention(t *testing.T) {
	membershipCases(t, []string{"policy_old", "policy_new"}, func(s *membershipScenario) {
		policySetup(s)
		s.exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_old USING(id>0) WITH CHECK(allowed)")
		oid := policyOID(s, "p")
		policyFacts(s, "p", "RESTRICTIVE", "UPDATE", "policy_old", "(id > 0)", "allowed")
		s.exec("ALTER POLICY p ON t")
		policySameOID(s, "p", oid)
		policyRoleBlocked(s, "policy_old", "p")
		policyColumnBlocked(s, "id", "p")
		policyColumnBlocked(s, "allowed", "p")
		s.exec("ALTER POLICY p ON t TO policy_new")
		policySameOID(s, "p", oid)
		s.exec("DROP ROLE policy_old")
		policyRoleBlocked(s, "policy_new", "p")
		policyColumnBlocked(s, "id", "p")
		policyColumnBlocked(s, "allowed", "p")
		s.exec("ALTER POLICY p ON t USING(id>1)")
		policySameOID(s, "p", oid)
		policyFacts(s, "p", "RESTRICTIVE", "UPDATE", "policy_new", "(id > 1)", "allowed")
		policyRoleBlocked(s, "policy_new", "p")
		policyColumnBlocked(s, "allowed", "p")
		s.exec("ALTER POLICY p ON t WITH CHECK(id<10)")
		policySameOID(s, "p", oid)
		policyFacts(s, "p", "RESTRICTIVE", "UPDATE", "policy_new", "(id > 1)", "(id < 10)")
		// Failed CHECK must not publish the preceding TO or USING replacement.
		q := "ALTER POLICY p ON t TO PUBLIC USING(TRUE) WITH CHECK(1)"
		policyError(s, q, "42804", "argument of POLICY must be type boolean, not type integer", "", 0)
		policySameOID(s, "p", oid)
		policyFacts(s, "p", "RESTRICTIVE", "UPDATE", "policy_new", "(id > 1)", "(id < 10)")
		policyRoleBlocked(s, "policy_new", "p")
		policyColumnBlocked(s, "id", "p")
		s.exec("ALTER POLICY p ON t RENAME TO renamed")
		policySameOID(s, "renamed", oid)
		policyRoleBlocked(s, "policy_new", "renamed")
		policyFacts(s, "renamed", "RESTRICTIVE", "UPDATE", "policy_new", "(id > 1)", "(id < 10)")
		policyError(s, "ALTER POLICY renamed ON t RENAME TO renamed", "42710", `policy "renamed" for table "t" already exists`, "", 0)
		s.exec("CREATE POLICY other ON t")
		policyError(s, "ALTER POLICY renamed ON t RENAME TO other", "42710", `policy "other" for table "t" already exists`, "", 0)
		s.exec("ALTER POLICY renamed ON t TO PUBLIC USING(TRUE) WITH CHECK(FALSE)")
		policySameOID(s, "renamed", oid)
		s.exec("DROP ROLE policy_new")
		policyFacts(s, "renamed", "RESTRICTIVE", "UPDATE", "public", "true", "false")
		s.exec("ALTER TABLE t ALTER COLUMN id TYPE BIGINT")
		s.exec("DROP POLICY renamed ON t")
	})
	// Omitted CHECK remains absent, not an eagerly stored USING fallback.
	membershipCases(t, nil, func(s *membershipScenario) {
		policySetup(s)
		s.exec("CREATE POLICY p ON t")
		policyFacts(s, "p", "PERMISSIVE", "ALL", "public", "", "")
		s.exec("ALTER POLICY p ON t USING(allowed)")
		policyFacts(s, "p", "PERMISSIVE", "ALL", "public", "allowed", "")
		s.exec("ALTER POLICY p ON t USING(id>0)")
		policyFacts(s, "p", "PERMISSIVE", "ALL", "public", "(id > 0)", "")
	})
}
