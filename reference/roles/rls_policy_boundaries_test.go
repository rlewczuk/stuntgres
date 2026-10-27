package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func policyTransactionWitnesses(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "transactional_ddl", true: "read_only"}[readOnly], func(t *testing.T) {
			fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				queries := []struct{ q, tag string }{{"CREATE POLICY p ON t", "CREATE POLICY"}, {"ALTER POLICY p ON t USING(TRUE)", "ALTER POLICY"}, {"ALTER POLICY p ON t RENAME TO renamed", "ALTER POLICY"}, {"DROP POLICY p ON t", "DROP POLICY"}}
				for _, tc := range queries {
					if tc.tag != "CREATE POLICY" {
						s.Exec("CREATE POLICY p ON t")
					}
					tx, err := s.C.BeginTx(s.P.Ctx, &sql.TxOptions{ReadOnly: readOnly})
					if err != nil {
						s.T.Fatal(err)
					}
					func() {
						defer tx.Rollback()
						var stmt *sql.Stmt
						if s.Prepared {
							stmt, err = tx.PrepareContext(s.P.Ctx, tc.q)
							if err != nil {
								s.T.Fatal("Prepare must not mutate/authorize", err)
							}
							defer stmt.Close()
						}
						var result sql.Result
						if stmt != nil {
							result, err = stmt.ExecContext(s.P.Ctx)
						} else {
							result, err = tx.ExecContext(s.P.Ctx, tc.q)
						}
						if readOnly {
							fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
						} else {
							fixture.State(s.T, err, "")
							fixture.LockAffected(s.T, result, 0)
							name := "p"
							if strings.Contains(tc.q, "RENAME") {
								name = "renamed"
							}
							want := int64(1)
							if tc.tag == "DROP POLICY" {
								want = 0
							}
							fixture.RunStep(s.T, s.P.Ctx, tx, s.Record, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='t'::regclass AND deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
							if want == 1 {
								rows, inquiryErr := tx.QueryContext(s.P.Ctx, "SELECT objid FROM pg_get_object_address('policy',ARRAY['public','t',$1],ARRAY[]::text[])", name)
								if inquiryErr != nil {
									s.T.Fatal(inquiryErr)
								}
								if !rows.Next() {
									s.T.Fatal("missing private identity", rows.Err())
								}
								var oid int64
								if inquiryErr = rows.Scan(&oid); inquiryErr != nil || oid <= 0 {
									s.T.Fatal(oid, inquiryErr)
								}
								if rows.Next() || rows.Err() != nil {
									s.T.Fatal("private identity tail", rows.Err())
								}
								rows.Close()
							}
						}
						if err != nil && result != nil {
							s.T.Fatal("error published result", result)
						}
					}()
					if tc.tag != "CREATE POLICY" {
						s.Exec("DROP POLICY p ON t")
					} else {
						s.Exec("CREATE POLICY p ON t")
						s.Exec("DROP POLICY p ON t")
					}
				}
				policyRows(s)
				// Read-only rejection precedes nonexistent target/role lookup.
				if readOnly {
					tx, err := s.C.BeginTx(s.P.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						s.T.Fatal(err)
					}
					defer tx.Rollback()
					step := fixture.ImplicitError("CREATE POLICY p ON missing TO missing_role", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE POLICY in a read-only transaction"})
					step.Prepared = s.Prepared
					step.ErrorStage = "execute"
					fixture.RunStep(s.T, s.P.Ctx, tx, s.Record, step)
				}
			})
		})
	}
	t.Run("persistent_to_temp", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE TEMP TABLE policy_temp_provider(id INTEGER)")
			s.Exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM policy_temp_provider WHERE policy_temp_provider.id=t.id))")
			policyRestrict(s, "DROP TABLE policy_temp_provider", "cannot drop table policy_temp_provider because other objects depend on it", "policy p on table t depends on table policy_temp_provider")
			if !s.Backend.PG {
				var out bytes.Buffer
				if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &out); err == nil || out.Len() != 0 {
					s.T.Fatal("persistent TEMP dependency exported or partially written", err, out.String())
				}
			}
			s.Exec("DROP TABLE policy_temp_provider CASCADE")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"})
			policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
			policyRows(s)
		})
	})
	t.Run("temp_to_persistent", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE TEMP TABLE policy_temp(id INTEGER)")
			s.Exec("CREATE POLICY temp_policy ON policy_temp USING(EXISTS(SELECT 1 FROM u))")
			policyRestrict(s, "DROP TABLE u", "cannot drop table u because other objects depend on it", "policy temp_policy on table policy_temp depends on table u")
			if !s.Backend.PG {
				before := policyDump(s)
				if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err == nil {
					s.T.Fatal("Reset invalidated live TEMP policy provider")
				}
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("")); err == nil {
					s.T.Fatal("Restore invalidated live TEMP policy provider")
				}
				if after := policyDump(s); after != before {
					s.T.Fatal("refused replacement changed persistent catalog")
				}
				s.Exec("ALTER POLICY temp_policy ON policy_temp USING(EXISTS(SELECT 1 FROM u))")
			}
			s.Exec("DROP TABLE u CASCADE")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy temp_policy on table policy_temp"})
			policyError(s, "ALTER POLICY temp_policy ON policy_temp", "42704", `policy "temp_policy" for table "policy_temp" does not exist`, "", 0)
			s.Run(fixture.Exec("INSERT INTO policy_temp VALUES(7)", 1))
			s.Run(fixture.Select("SELECT id FROM policy_temp", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			policyRows(s)
		})
	})
}
