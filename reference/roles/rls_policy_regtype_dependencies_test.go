package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Independent expectations: PostgreSQL REL_18_0 dependency.c:1760-1810
// records the existing type represented by a non-NULL REGTYPE Const, even
// without a cast to that type or a relational source. dependency.c:1050-1180
// reports the immediate dependee, and objectaddress.c:2956-2966 labels a row
// type as "type", not "table". The pinned run must establish these complete
// diagnostics before the native implementation is remedied.
type policyRegtypeProvider struct {
	name, create, drop, subject string
}

func policyRegtypeProviders() []policyRegtypeProvider {
	return []policyRegtypeProvider{
		{"domain", "CREATE DOMAIN policy_regtype_provider AS INTEGER", "DROP DOMAIN policy_regtype_provider", "type policy_regtype_provider"},
		{"row_type", "CREATE TABLE policy_regtype_provider(id INTEGER)", "DROP TABLE policy_regtype_provider", "table policy_regtype_provider"},
	}
}

func policyRegtypeClauses(clause, expression string) string {
	if clause == "using" {
		return " USING(" + expression + ") WITH CHECK(TRUE)"
	}
	return " USING(TRUE) WITH CHECK(" + expression + ")"
}

func policyRegtypeAlter(clause, expression string) string {
	if clause == "using" {
		return "USING(" + expression + ")"
	}
	return "WITH CHECK(" + expression + ")"
}

func policyRegtypeBlocked(s *fixture.RoleMembershipScenario, provider policyRegtypeProvider, policy, owner string) {
	s.T.Helper()
	policyRestrict(s, provider.drop+" RESTRICT", "cannot drop "+provider.subject+" because other objects depend on it", "policy "+policy+" on table "+owner+" depends on type policy_regtype_provider")
}

func TestReferenceRLSPolicyRegtypeDependencies(t *testing.T) {
	for _, provider := range policyRegtypeProviders() {
		t.Run(provider.name, func(t *testing.T) {
			for _, clause := range []string{"using", "check"} {
				t.Run(clause, func(t *testing.T) {
					fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
						policySetup(s)
						s.Exec(provider.create)
						expression := "'policy_regtype_provider'::regtype IS NOT NULL"
						s.Exec("CREATE POLICY p ON t" + policyRegtypeClauses(clause, expression))
						policyRegtypeBlocked(s, provider, "p", "t")
						policyRows(s)

						// Selectively replacing the opposite slot must not release
						// the REGTYPE identity in the omitted slot.
						opposite := "using"
						if clause == "using" {
							opposite = "check"
						}
						s.Exec("ALTER POLICY p ON t " + policyRegtypeAlter(opposite, "FALSE"))
						policyRegtypeBlocked(s, provider, "p", "t")
						s.Exec("ALTER POLICY p ON t TO PUBLIC")
						policyRegtypeBlocked(s, provider, "p", "t")

						// Replacing the referencing slot releases its old edge.
						s.Exec("ALTER POLICY p ON t " + policyRegtypeAlter(clause, "TRUE"))
						s.Exec(provider.drop + " RESTRICT")
						s.Exec("ALTER POLICY p ON t")
						policyRows(s)
						s.Exec("DROP POLICY p ON t")

						// Dropping a policy also releases the represented identity;
						// no view, typed column, cast or query source shares the edge.
						s.Exec(provider.create)
						s.Exec("CREATE POLICY p ON t" + policyRegtypeClauses(clause, expression))
						policyRegtypeBlocked(s, provider, "p", "t")
						s.Exec("DROP POLICY p ON t")
						s.Exec(provider.drop + " RESTRICT")
						policyRows(s)

						// CASCADE removes only the dependent policy, not its owner
						// table, its rows, or an independent sibling policy.
						s.Exec(provider.create)
						s.Exec("CREATE POLICY p ON t" + policyRegtypeClauses(clause, expression))
						s.Exec("CREATE POLICY retained ON t USING(NULL::regtype IS NULL) WITH CHECK('integer'::regtype IS NOT NULL)")
						policyRegtypeBlocked(s, provider, "p", "t")
						s.Exec(provider.drop + " CASCADE")
						policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"})
						policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
						s.Exec("ALTER POLICY retained ON t")
						policyRows(s)
						// Successful same-name recreation proves the provider was
						// actually removed, rather than merely hiding the policy.
						s.Exec(provider.create)
						s.Exec(provider.drop + " RESTRICT")
						s.Exec("DROP POLICY retained ON t")
					})
				})
			}
		})
	}
}

func policyRegtypeTempRows(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT id FROM policy_regtype_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceRLSPolicyRegtypeTemporary(t *testing.T) {
	for _, provider := range policyRegtypeProviders() {
		t.Run("persistent_"+provider.name, func(t *testing.T) {
			for _, clause := range []string{"using", "check"} {
				t.Run(clause, func(t *testing.T) {
					fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
						policySetup(s)
						s.Exec(provider.create)
						s.Exec("CREATE TEMP TABLE policy_regtype_temp(id INTEGER)")
						s.Run(fixture.Exec("INSERT INTO policy_regtype_temp VALUES(7)", 1))
						s.Exec("CREATE POLICY temp_policy ON policy_regtype_temp" + policyRegtypeClauses(clause, "'policy_regtype_provider'::regtype IS NOT NULL"))
						policyRegtypeBlocked(s, provider, "temp_policy", "policy_regtype_temp")
						if !s.Backend.PG {
							before := policyDump(s)
							// Each control must refuse before publication: even the
							// identical emitted stream has new persistent identities.
							for _, control := range []string{"reset", "empty_restore", "same_name_restore"} {
								var err error
								if control == "reset" {
									err = stuntgres.Reset(s.P.Ctx, s.P.Native)
								} else {
									stream := ""
									if control == "same_name_restore" {
										stream = before
									}
									err = stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(stream))
								}
								if want := "stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported"; err == nil || err.Error() != want {
									s.T.Fatalf("%s refusal got %v want %s", control, err, want)
								}
								if after := policyDump(s); after != before {
									s.T.Fatalf("%s refusal changed persistent catalog/data", control)
								}
								policyRows(s)
								policyRegtypeTempRows(s)
								policyRegtypeBlocked(s, provider, "temp_policy", "policy_regtype_temp")
							}
						}
						policyRows(s)
						policyRegtypeTempRows(s)
						s.Exec("DROP POLICY temp_policy ON policy_regtype_temp")
						s.Exec(provider.drop + " RESTRICT")
						policyRegtypeTempRows(s)
					})
				})
			}
		})
	}
	t.Run("retained_temp_row_type", func(t *testing.T) {
		for _, clause := range []string{"using", "check"} {
			t.Run(clause, func(t *testing.T) {
				fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
					policySetup(s)
					provider := policyRegtypeProvider{"row_type", "CREATE TEMP TABLE policy_regtype_provider(id INTEGER)", "DROP TABLE policy_regtype_provider", "table policy_regtype_provider"}
					s.Exec(provider.create)
					s.Run(fixture.Exec("INSERT INTO policy_regtype_provider VALUES(11)", 1))
					s.Exec("CREATE TEMP TABLE policy_regtype_temp(id INTEGER)")
					s.Run(fixture.Exec("INSERT INTO policy_regtype_temp VALUES(7)", 1))
					s.Exec("CREATE POLICY temp_policy ON policy_regtype_temp" + policyRegtypeClauses(clause, "'policy_regtype_provider'::regtype IS NOT NULL"))
					policyRegtypeBlocked(s, provider, "temp_policy", "policy_regtype_temp")
					if !s.Backend.PG {
						before := policyDump(s)
						if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
							s.T.Fatal("compatible TEMP REGTYPE Reset", err)
						}
						policyRegtypeTempRows(s)
						policyRegtypeBlocked(s, provider, "temp_policy", "policy_regtype_temp")
						if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before)); err != nil {
							s.T.Fatal("compatible TEMP REGTYPE Restore", err)
						}
						if after := policyDump(s); after != before {
							s.T.Fatal("compatible Restore changed persistent stream")
						}
					}
					policyRows(s)
					policyRegtypeTempRows(s)
					s.Run(fixture.Select("SELECT id FROM policy_regtype_provider ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}))
					policyRegtypeBlocked(s, provider, "temp_policy", "policy_regtype_temp")
					s.Exec("ALTER POLICY temp_policy ON policy_regtype_temp " + policyRegtypeAlter(clause, "TRUE"))
					s.Exec(provider.drop + " RESTRICT")
					s.Exec("ALTER POLICY temp_policy ON policy_regtype_temp")
					policyRegtypeTempRows(s)
				})
			})
		}
	})
}
