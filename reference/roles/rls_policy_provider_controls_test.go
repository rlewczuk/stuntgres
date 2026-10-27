package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// No expression below has a relational query source: each blocker is carried
// exclusively by the policy's expression-provider manifest.
func TestReferenceRLSPolicyProviderControls(t *testing.T) {
	providers := []struct {
		name, setup, temporarySetup, expr, temporaryExpr, drop, subject string
	}{
		{"domain", "CREATE DOMAIN policy_provider AS INTEGER", "CREATE DOMAIN pg_temp.policy_provider AS INTEGER", "id::public.policy_provider>0", "id::pg_temp.policy_provider>0", "DROP DOMAIN ", "type "},
		{"collation", "CREATE COLLATION policy_provider FROM \"C\"", "CREATE COLLATION pg_temp.policy_provider FROM \"C\"", "'a' COLLATE public.policy_provider='a'", "'a' COLLATE pg_temp.policy_provider='a'", "DROP COLLATION ", "collation "},
		{"sequence", "CREATE SEQUENCE policy_provider", "CREATE TEMP SEQUENCE policy_provider", "nextval('public.policy_provider')>0", "nextval('pg_temp.policy_provider')>0", "DROP SEQUENCE ", "sequence "},
	}
	for _, provider := range providers {
		for _, clause := range []string{"USING", "WITH CHECK"} {
			for _, temporary := range []bool{false, true} {
				mode := "persistent_refusal"
				if temporary {
					mode = "temporary_retention"
					if provider.name == "domain" {
						mode = "temporary_capability"
					}
				}
				t.Run(provider.name+"/"+clause+"/"+mode, func(t *testing.T) {
					fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
						s.Exec("CREATE TABLE sentinel(id INTEGER)")
						s.Run(fixture.Exec("INSERT INTO sentinel VALUES(91)", 1))
						s.Exec("CREATE TEMP TABLE policy_temp(id INTEGER)")
						s.Run(fixture.Exec("INSERT INTO policy_temp VALUES(7),(8)", 2))
						setup, expr, qualified := provider.setup, provider.expr, "public.policy_provider"
						if temporary {
							setup, expr, qualified = provider.temporarySetup, provider.temporaryExpr, "pg_temp.policy_provider"
						}
						if temporary && provider.name == "domain" && !s.Backend.PG {
							// This is an adjacent CREATE capability boundary, not a
							// successful policy retention/control witness. PostgreSQL
							// still runs the full TEMP provider dependency case below.
							step := fixture.ImplicitError(setup, fixture.Diagnostic{Code: "0A000", Message: "temporary schema creation is unsupported"})
							step.ErrorStage = "execute"
							s.Run(step)
							s.Run(fixture.Select("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
							s.Run(fixture.Select("SELECT id FROM policy_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
							return
						}
						s.Exec(setup)
						s.Exec("CREATE POLICY temp_policy ON policy_temp " + clause + "(" + expr + ")")
						subject := provider.subject + "policy_provider"
						if temporary && provider.name == "collation" {
							// Collations are not implicitly visible through the TEMP
							// search path. Derive their displayed namespace from an
							// independent catalog fact, never from error output.
							var namespace string
							if s.Backend.PG {
								if err := s.C.QueryRowContext(s.P.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&namespace); err != nil {
									s.T.Fatal(err)
								}
							} else {
								// Native current_schema resolves the explicit TEMP alias
								// to its actual namespace without requiring pg_namespace.
								s.Exec("SET search_path=pg_temp")
								if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_schema()").Scan(&namespace); err != nil {
									s.T.Fatal(err)
								}
								s.Exec("RESET search_path")
							}
							if namespace == "" || namespace == "pg_temp" {
								s.T.Fatal("expected actual temporary namespace", namespace)
							}
							subject = provider.subject + namespace + ".policy_provider"
						}
						unchanged := func() {
							metadata := pgPolicyStructuralRow(pgPolicyRow("policy_temp", "temp_policy", "*", true, "public", "", ""))
							metadata.Query = strings.ReplaceAll(metadata.Query, "ARRAY['public','policy_temp','temp_policy']", "ARRAY['pg_temp','policy_temp','temp_policy']")
							if clause == "USING" {
								metadata.Rows[0][6] = true
							} else {
								metadata.Rows[0][7] = true
							}
							s.Run(metadata)
							s.Run(fixture.Select("SELECT id FROM sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(91)}}))
							s.Run(fixture.Select("SELECT id FROM policy_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
							policyRestrict(s, provider.drop+qualified, "cannot drop "+subject+" because other objects depend on it", "policy temp_policy on table policy_temp depends on "+subject)
							if provider.name == "sequence" {
								s.Run(fixture.Select("SELECT last_value,is_called FROM "+qualified, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
							}
						}
						unchanged()
						if !s.Backend.PG {
							before := policyDump(s)
							if temporary {
								// TEMP objects are absent from the stream but must survive
								// replacement, including both condition-provider slots.
								for range 2 {
									if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before)); err != nil {
										s.T.Fatal("compatible TEMP provider Restore", err)
									}
									if policyDump(s) != before {
										s.T.Fatal("TEMP retention changed persistent redump")
									}
									unchanged()
								}
								if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
									s.T.Fatal("compatible TEMP provider Reset", err)
								}
								s.Run(fixture.Select("SELECT id FROM policy_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
								policyRestrict(s, provider.drop+qualified, "cannot drop "+subject+" because other objects depend on it", "policy temp_policy on table policy_temp depends on "+subject)
								if provider.name == "sequence" {
									s.Run(fixture.Select("SELECT last_value,is_called FROM "+qualified, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
								}
								// Restore the real persistent stream after Reset, still
								// preserving the same live TEMP policy/provider identities.
								if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before)); err != nil {
									s.T.Fatal("compatible TEMP provider Restore after Reset", err)
								}
								unchanged()
							} else {
								for _, operation := range []string{"Reset", "same_name_Restore"} {
									var err error
									if operation == "Reset" {
										err = stuntgres.Reset(s.P.Ctx, s.P.Native)
									} else {
										// Actual emitted definitions recreate the same provider
										// name, not its retained immutable identity.
										err = stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before))
									}
									if err == nil || err.Error() != "stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported" {
										s.T.Fatal(operation, "did not refuse incompatible provider", err)
									}
									if policyDump(s) != before {
										s.T.Fatal(operation, "changed persistent sentinel/provider definitions")
									}
									unchanged()
								}
							}
						}
						// Shared PostgreSQL/native release proof also detects a silently
						// lost retained policy or provider dependency.
						s.Exec("DROP POLICY temp_policy ON policy_temp")
						s.Exec(provider.drop + qualified)
						s.Run(fixture.Select("SELECT id FROM policy_temp ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
					})
				})
			}
		}
	}
}
