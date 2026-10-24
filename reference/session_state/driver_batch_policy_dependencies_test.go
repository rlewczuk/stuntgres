package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDriverBatchPolicyProviders(t *testing.T) {
	providers := []struct{ name, setup, expr, drop, subject string }{
		{"table", "CREATE TABLE policy_provider(id INT)", "EXISTS(SELECT 1 FROM policy_provider)", "DROP TABLE policy_provider", "table policy_provider"},
		{"view", "CREATE VIEW policy_provider AS SELECT id FROM policy_t", "EXISTS(SELECT 1 FROM policy_provider)", "DROP VIEW policy_provider", "view policy_provider"},
		{"domain", "CREATE DOMAIN policy_provider AS INT", "id::policy_provider>0", "DROP DOMAIN policy_provider", "type policy_provider"},
		{"collation", "CREATE COLLATION policy_provider FROM \"C\"", "'a' COLLATE policy_provider='a'", "DROP COLLATION policy_provider", "collation policy_provider"},
		{"sequence", "CREATE SEQUENCE policy_provider", "nextval('policy_provider')>0", "DROP SEQUENCE policy_provider", "sequence policy_provider"},
	}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			for _, private := range []bool{false, true} {
				t.Run(map[bool]string{false: "committed_provider", true: "private_provider"}[private], func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"batch_policy_target"})
					batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
						batchPolicySeed(t, p, b, a)
						setup := tc.setup + "; CREATE ROLE batch_policy_target LOGIN; CREATE POLICY p ON policy_t TO batch_policy_target USING(" + tc.expr + ") WITH CHECK(" + tc.expr + ")"
						w := batchDiag("2BP01", "cannot drop "+tc.subject+" because other objects depend on it")
						w.Detail = "policy p on table policy_t depends on " + tc.subject
						w.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
						if private {
							// USING is retired, but retained CHECK must still block the
							// private provider DROP and roll back every new root.
							batchError(t, p, a, setup+"; ALTER POLICY p ON policy_t USING(TRUE); "+tc.drop, w)
							batchPolicyMissing(t, p, a, "p")
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_user WHERE usename='batch_policy_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							// Same-name creation is a real provider absence witness.
							batchExec(t, p, b, a, setup+"; ALTER POLICY p ON policy_t USING(TRUE); ALTER POLICY p ON policy_t WITH CHECK(TRUE); "+tc.drop+"; ALTER POLICY p ON policy_t TO PUBLIC; DROP ROLE batch_policy_target; DROP POLICY p ON policy_t", 0)
						} else {
							batchExec(t, p, b, a, tc.setup, 0)
							batchExec(t, p, b, a, "CREATE ROLE batch_policy_target LOGIN", 0)
							batchExec(t, p, b, a, "CREATE POLICY p ON policy_t TO batch_policy_target USING("+tc.expr+") WITH CHECK("+tc.expr+")", 0)
							var oid int64
							before := ""
							if b.PG {
								oid = batchPolicyOID(t, p, a, "p", "batch_policy_target")
							} else {
								before = batchPolicyDump(t, p)
							}
							batchError(t, p, a, "ALTER POLICY p ON policy_t USING(TRUE); "+tc.drop, w)
							batchError(t, p, a, tc.drop, w)
							retire := "ALTER POLICY p ON policy_t USING(TRUE); ALTER POLICY p ON policy_t WITH CHECK(TRUE); " + tc.drop + "; ALTER POLICY p ON policy_t TO PUBLIC; DROP ROLE batch_policy_target; DROP POLICY p ON policy_t"
							q := retire + "; SELECT missing FROM policy_t"
							missing := batchDiag("42703", `column "missing" does not exist`)
							missing.Position = batchPosition(q, "missing")
							batchError(t, p, a, q, missing)
							batchError(t, p, a, tc.drop, w)
							if b.PG {
								if batchPolicyOID(t, p, peer, "p", "batch_policy_target") != oid {
									t.Fatal("rollback changed provider policy OID")
								}
							} else if batchPolicyDump(t, p) != before {
								t.Fatal("late retirement failure changed committed provider/policy export")
							}
							batchExec(t, p, b, a, retire, 0)
						}
						batchPolicyMissing(t, p, peer, "p")
						// Retired names can be reused only after all dependency edges
						// and provider roots have actually been released.
						batchExec(t, p, b, peer, tc.setup, 0)
						batchExec(t, p, b, peer, tc.drop, 0)
						batchPolicyRows(t, p, b, peer)
					})
				})
			}
		})
	}
}

func TestReferenceDriverBatchPolicyPreparedParameters(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"batch_policy_target"})
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchPolicySeed(t, p, b, a)
		batchExec(t, p, b, a, "CREATE ROLE batch_policy_target LOGIN", 0)
		batchExec(t, p, b, a, "CREATE POLICY p ON policy_t TO batch_policy_target USING(id>0) WITH CHECK(allowed)", 0)
		var oid int64
		before := ""
		if b.PG {
			oid = batchPolicyOID(t, p, a, "p", "batch_policy_target")
		} else {
			before = batchPolicyDump(t, p)
		}
		for _, q := range []string{
			"INSERT INTO policy_t VALUES(3,TRUE); CREATE POLICY new_policy ON policy_t USING(TRUE)",
			"INSERT INTO policy_t VALUES(3,TRUE); ALTER POLICY p ON policy_t USING(FALSE)",
			"INSERT INTO policy_t VALUES(3,TRUE); DROP POLICY p ON policy_t",
		} {
			stmt, err := a.PrepareContext(p.Ctx, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("policy batch exposed prepared handle")
			}
			w := batchDiag("42601", "cannot insert multiple commands into a prepared statement")
			fixture.AssertDiagnostic(t, err, &w)
			result, err := a.ExecContext(p.Ctx, q, 3)
			if result != nil {
				t.Fatal("argument-bearing policy batch returned result")
			}
			fixture.AssertDiagnostic(t, err, &w)
			batchPolicyRows(t, p, b, peer)
			if b.PG {
				if batchPolicyOID(t, p, peer, "p", "batch_policy_target") != oid {
					t.Fatal("rejected batch changed policy OID")
				}
			} else if batchPolicyDump(t, p) != before {
				t.Fatal("rejected batch changed committed export")
			}
			batchPolicyMissing(t, p, a, "new_policy")
		}
		batchExec(t, p, b, a, "DROP POLICY p ON policy_t; DROP ROLE batch_policy_target", 0)
	})
}

func TestReferenceDriverBatchPolicyExplicitBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"batch_explicit_reader"})
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchPolicySeed(t, p, b, a)
		batchExec(t, p, b, a, "CREATE POLICY p ON policy_t USING(TRUE)", 0)
		before := ""
		if !b.PG {
			before = batchPolicyDump(t, p)
		}
		batchExec(t, p, b, a, "BEGIN", 0)
		batchExec(t, p, b, a, "ALTER POLICY p ON policy_t USING(FALSE)", 0)
		if !b.PG && batchPolicyDump(t, p) != before {
			t.Fatal("private explicit mutation changed committed export")
		}
		batchExec(t, p, b, a, "ROLLBACK", 0)
		if !b.PG && batchPolicyDump(t, p) != before {
			t.Fatal("rolled-back explicit mutation changed committed export")
		}
		for _, q := range []string{"CREATE ROLE batch_explicit_reader", "GRANT USAGE ON SCHEMA public TO batch_explicit_reader", "GRANT SELECT ON policy_t TO batch_explicit_reader", "ALTER TABLE policy_t ENABLE ROW LEVEL SECURITY"} {
			batchExec(t, p, b, a, q, 0)
		}
		batchExec(t, p, b, a, "BEGIN", 0)
		batchExec(t, p, b, a, "ALTER POLICY p ON policy_t USING(FALSE)", 0)
		batchExec(t, p, b, a, "SET ROLE batch_explicit_reader", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT id FROM policy_t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{}))
		batchExec(t, p, b, a, "RESET ROLE", 0)
		batchExec(t, p, b, a, "ROLLBACK", 0)
		if b.PG {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT qual FROM pg_policies WHERE tablename='policy_t' AND policyname='p'", []string{"qual"}, []string{"TEXT"}, [][]any{{"true"}}))
		}
		batchExec(t, p, b, peer, "SET ROLE batch_explicit_reader", 0)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id FROM policy_t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		batchExec(t, p, b, peer, "RESET ROLE", 0)
		batchPolicyRows(t, p, b, peer)
	})
}
