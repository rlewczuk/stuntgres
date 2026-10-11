package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc_actor_direct", "pc_actor_prepared"})
	partitionConstraintCase(t, "parent_owner_and_independent_leaf_rights", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		actor := "pc_actor_direct"
		if prepared {
			actor = "pc_actor_prepared"
		}
		setup := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(setup)
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+actor, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
		}
		run(fixture.Exec("GRANT INSERT ON pc_a TO "+actor, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
		run(referenceRangeError("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", "42501", "must be owner of index pc_root_idx", fixture.Diagnostic{}))
		run(referenceRangeError("INSERT INTO pc_root VALUES(1,'denied')", "42501", "permission denied for table pc_root", fixture.Diagnostic{}))
		run(fixture.Exec("INSERT INTO pc_a VALUES(1,'allowed')", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "allowed"}}))
		run(fixture.Exec("REVOKE INSERT ON pc_a FROM "+actor, 0))
		run(fixture.Exec("GRANT INSERT ON pc_root TO "+actor, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
		run(referenceRangeError("INSERT INTO pc_a VALUES(2,'denied')", "42501", "permission denied for table pc_a", fixture.Diagnostic{}))
		run(fixture.Exec("INSERT INTO pc_root VALUES(2,'root_right')", 1))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferencePartitionConstraintsPrivacy(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc_actor_direct", "pc_actor_prepared"})
	partitionConstraintCase(t, "physical_leaf_disclosure", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		actor := "pc_actor_direct"
		if prepared {
			actor = "pc_actor_prepared"
		}
		setup := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(setup, "UNIQUE(k,payload)")
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE "+actor, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
		}
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'secret')", 1))
		run(fixture.Exec("GRANT INSERT,SELECT ON pc_root TO "+actor, 0))
		for _, mode := range []string{"parent_only", "one_leaf_column", "all_leaf_columns", "revoked", "table_leaf_select", "leaf_rls"} {
			switch mode {
			case "one_leaf_column":
				run(fixture.Exec("GRANT SELECT(k) ON pc_a TO "+actor, 0))
			case "all_leaf_columns":
				run(fixture.Exec("GRANT SELECT(payload) ON pc_a TO "+actor, 0))
			case "revoked":
				run(fixture.Exec("REVOKE SELECT(k,payload) ON pc_a FROM "+actor, 0))
			case "table_leaf_select":
				run(fixture.Exec("GRANT SELECT ON pc_a TO "+actor, 0))
			case "leaf_rls":
				run(fixture.Exec("ALTER TABLE pc_a ENABLE ROW LEVEL SECURITY", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION "+actor, 0))
			detail := ""
			if mode == "all_leaf_columns" || mode == "table_leaf_select" {
				detail = "Key (k, payload)=(1, secret) already exists."
			}
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES($1,$2)", "pc_a", "pc_a_k_payload_key", detail, 1, "secret"))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("ALTER TABLE pc_a DISABLE ROW LEVEL SECURITY", 0))
		run(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "secret"}}))
	})
}
