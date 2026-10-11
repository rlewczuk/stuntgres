package reference_test

import "testing"

// REL_18_0 gram.y accepts IF EXISTS for rename only. policy.c rename_policy
// deliberately uses ordinary lookup and does not consume missing_ok. These
// source-grounded expectations require selected pinned confirmation by the root.
func TestReferenceRLSPolicyConditionalRename(t *testing.T) {
	t.Run("retention_and_absence", func(t *testing.T) {
		membershipCases(t, []string{"policy_rename_target"}, func(s *membershipScenario) {
			policySetup(s)
			s.exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_rename_target USING(id>0) WITH CHECK(allowed)")
			s.exec("ALTER POLICY IF EXISTS p ON t RENAME TO q")
			policyRoleBlocked(s, "policy_rename_target", "q")
			policyError(s, "ALTER POLICY IF EXISTS q ON t RENAME TO q", "42710", `policy "q" for table "t" already exists`, "", 0)
			policyError(s, "ALTER POLICY IF EXISTS p ON t RENAME TO absent", "42704", `policy "p" for table "t" does not exist`, "", 0)
			policyError(s, "ALTER POLICY IF EXISTS p ON missing RENAME TO absent", "42P01", `relation "missing" does not exist`, "", 0)
			s.exec("DROP POLICY q ON t")
			s.exec("DROP ROLE policy_rename_target")
			policyRows(s)
		})
	})
	t.Run("owner_before_missing", func(t *testing.T) {
		membershipCases(t, []string{"policy_rename_reader"}, func(s *membershipScenario) {
			policySetup(s)
			s.exec("CREATE POLICY p ON t")
			s.exec("GRANT SELECT ON t TO policy_rename_reader")
			if s.backend.PG {
				s.exec("GRANT USAGE ON SCHEMA public TO policy_rename_reader")
			}
			s.actor("policy_rename_reader")
			policyError(s, "ALTER POLICY IF EXISTS p ON public.t RENAME TO q", "42501", "must be owner of table t", "", 0)
			policyError(s, "ALTER POLICY IF EXISTS absent ON public.t RENAME TO q", "42501", "must be owner of table t", "", 0)
			s.actor("")
			s.exec("DROP POLICY p ON t")
			policyRows(s)
		})
	})
}
