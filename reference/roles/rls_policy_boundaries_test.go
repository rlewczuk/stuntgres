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
			membershipCases(t, nil, func(s *membershipScenario) {
				policySetup(s)
				queries := []struct{ q, tag string }{{"CREATE POLICY p ON t", "CREATE POLICY"}, {"ALTER POLICY p ON t USING(TRUE)", "ALTER POLICY"}, {"ALTER POLICY p ON t RENAME TO renamed", "ALTER POLICY"}, {"DROP POLICY p ON t", "DROP POLICY"}}
				for _, tc := range queries {
					if tc.tag != "CREATE POLICY" {
						s.exec("CREATE POLICY p ON t")
					}
					tx, err := s.c.BeginTx(s.p.Ctx, &sql.TxOptions{ReadOnly: readOnly})
					if err != nil {
						s.t.Fatal(err)
					}
					func() {
						defer tx.Rollback()
						var stmt *sql.Stmt
						if s.prepared {
							stmt, err = tx.PrepareContext(s.p.Ctx, tc.q)
							if err != nil {
								s.t.Fatal("Prepare must not mutate/authorize", err)
							}
							defer stmt.Close()
						}
						var result sql.Result
						if stmt != nil {
							result, err = stmt.ExecContext(s.p.Ctx)
						} else {
							result, err = tx.ExecContext(s.p.Ctx, tc.q)
						}
						if readOnly {
							fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"})
						} else {
							fixture.State(s.t, err, "")
							fixture.LockAffected(s.t, result, 0)
							name := "p"
							if strings.Contains(tc.q, "RENAME") {
								name = "renamed"
							}
							want := int64(1)
							if tc.tag == "DROP POLICY" {
								want = 0
							}
							fixture.RunStep(s.t, s.p.Ctx, tx, s.record, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=3256 AND refobjid='t'::regclass AND deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{want}}))
							if want == 1 {
								rows, inquiryErr := tx.QueryContext(s.p.Ctx, "SELECT objid FROM pg_get_object_address('policy',ARRAY['public','t',$1],ARRAY[]::text[])", name)
								if inquiryErr != nil {
									s.t.Fatal(inquiryErr)
								}
								if !rows.Next() {
									s.t.Fatal("missing private identity", rows.Err())
								}
								var oid int64
								if inquiryErr = rows.Scan(&oid); inquiryErr != nil || oid <= 0 {
									s.t.Fatal(oid, inquiryErr)
								}
								if rows.Next() || rows.Err() != nil {
									s.t.Fatal("private identity tail", rows.Err())
								}
								rows.Close()
							}
						}
						if err != nil && result != nil {
							s.t.Fatal("error published result", result)
						}
					}()
					if tc.tag != "CREATE POLICY" {
						s.exec("DROP POLICY p ON t")
					} else {
						s.exec("CREATE POLICY p ON t")
						s.exec("DROP POLICY p ON t")
					}
				}
				policyRows(s)
				// Read-only rejection precedes nonexistent target/role lookup.
				if readOnly {
					tx, err := s.c.BeginTx(s.p.Ctx, &sql.TxOptions{ReadOnly: true})
					if err != nil {
						s.t.Fatal(err)
					}
					defer tx.Rollback()
					step := fixture.ImplicitError("CREATE POLICY p ON missing TO missing_role", fixture.Diagnostic{Code: "25006", Message: "cannot execute CREATE POLICY in a read-only transaction"})
					step.Prepared = s.prepared
					step.ErrorStage = "execute"
					fixture.RunStep(s.t, s.p.Ctx, tx, s.record, step)
				}
			})
		})
	}
	t.Run("persistent_to_temp", func(t *testing.T) {
		membershipCases(t, nil, func(s *membershipScenario) {
			policySetup(s)
			s.exec("CREATE TEMP TABLE policy_temp_provider(id INTEGER)")
			s.exec("CREATE POLICY p ON t USING(EXISTS(SELECT 1 FROM policy_temp_provider WHERE policy_temp_provider.id=t.id))")
			policyRestrict(s, "DROP TABLE policy_temp_provider", "cannot drop table policy_temp_provider because other objects depend on it", "policy p on table t depends on table policy_temp_provider")
			if !s.backend.PG {
				var out bytes.Buffer
				if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &out); err == nil || out.Len() != 0 {
					s.t.Fatal("persistent TEMP dependency exported or partially written", err, out.String())
				}
			}
			s.exec("DROP TABLE policy_temp_provider CASCADE")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy p on table t"})
			policyError(s, "ALTER POLICY p ON t", "42704", `policy "p" for table "t" does not exist`, "", 0)
			policyRows(s)
		})
	})
	t.Run("temp_to_persistent", func(t *testing.T) {
		membershipCases(t, nil, func(s *membershipScenario) {
			policySetup(s)
			s.exec("CREATE TEMP TABLE policy_temp(id INTEGER)")
			s.exec("CREATE POLICY temp_policy ON policy_temp USING(EXISTS(SELECT 1 FROM u))")
			policyRestrict(s, "DROP TABLE u", "cannot drop table u because other objects depend on it", "policy temp_policy on table policy_temp depends on table u")
			if !s.backend.PG {
				before := policyDump(s)
				if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err == nil {
					s.t.Fatal("Reset invalidated live TEMP policy provider")
				}
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("")); err == nil {
					s.t.Fatal("Restore invalidated live TEMP policy provider")
				}
				if after := policyDump(s); after != before {
					s.t.Fatal("refused replacement changed persistent catalog")
				}
				s.exec("ALTER POLICY temp_policy ON policy_temp USING(EXISTS(SELECT 1 FROM u))")
			}
			s.exec("DROP TABLE u CASCADE")
			policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to policy temp_policy on table policy_temp"})
			policyError(s, "ALTER POLICY temp_policy ON policy_temp", "42704", `policy "temp_policy" for table "policy_temp" does not exist`, "", 0)
			s.run(fixture.Exec("INSERT INTO policy_temp VALUES(7)", 1))
			s.run(fixture.Select("SELECT id FROM policy_temp", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			policyRows(s)
		})
	})
}
