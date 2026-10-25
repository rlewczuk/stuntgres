package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsAdmission(t *testing.T) {
	for _, probe := range []struct{ name, query, code, message, hint string }{
		{"missing_parent", "ALTER INDEX pc_missing ATTACH PARTITION pc_a_idx", "42P01", `relation "pc_missing" does not exist`, ""},
		{"missing_child", "ALTER INDEX pc_root_idx ATTACH PARTITION pc_missing", "42P01", `relation "pc_missing" does not exist`, ""},
		{"missing_schema", "ALTER INDEX pc_missing_schema.pc_root_idx ATTACH PARTITION pc_a_idx", "3F000", `schema "pc_missing_schema" does not exist`, ""},
		{"wrong_parent_kind", "ALTER INDEX pc_root ATTACH PARTITION pc_a_idx", "42809", `"pc_root" is not an index`, ""},
		{"wrong_child_kind", "ALTER INDEX pc_root_idx ATTACH PARTITION pc_a", "42P17", `"pc_a" is not an index`, ""},
		{"late_missing_drop", "DROP INDEX pc_root_idx,pc_missing", "42704", `index "pc_missing" does not exist`, ""},
		{"late_wrong_kind_drop", "DROP INDEX pc_root_idx,pc_a", "42809", `"pc_a" is not an index`, "Use DROP TABLE to remove a table."},
	} {
		partitionConstraintCase(t, probe.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			partitionConstraintManualTree(run)
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
			run(referenceRangeError(probe.query, probe.code, probe.message, fixture.Diagnostic{Hint: probe.hint}))
			run(partitionConstraintIndexState("pc_root_idx", false))
			run(fixture.Select("SELECT deptype FROM pg_depend WHERE objid='pc_a_idx'::regclass::OID AND deptype='P'", []string{"deptype"}, []string{"CHAR"}, nil))
			run(fixture.Select("SELECT k,payload FROM pc_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_a_idx", "Key (k, payload)=(1, sentinel) already exists."))
			run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
			run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		})
	}
}

func TestReferencePartitionConstraintsHeldCurrentRole(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pc_held_actor_direct", "pc_held_actor_prepared"})
	partitionConstraintCase(t, "same_prepared_utility_and_insert", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		setup := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(setup)
		actor := "pc_held_actor_direct"
		if prepared {
			actor = "pc_held_actor_prepared"
		}
		connection, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer connection.ExecContext(p.Ctx, "RESET ROLE")
		run := func(step fixture.Step) {
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, connection, b.Recorder, step)
		}
		run(fixture.Exec("CREATE ROLE "+actor, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+actor, 0))
		}
		run(fixture.Exec("GRANT INSERT ON pc_root TO "+actor, 0))
		attachSQL := "ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx"
		attach := fixture.IdentityHeld(t, p.Ctx, connection, attachSQL)
		insert := fixture.IdentityHeld(t, p.Ctx, connection, "INSERT INTO pc_root VALUES($1,$2)")
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(fixture.Select("SELECT deptype FROM pg_depend WHERE objid='pc_a_idx'::regclass::OID AND deptype='P'", []string{"deptype"}, []string{"CHAR"}, nil))
		run(fixture.Exec("SET ROLE "+actor, 0))
		fixture.RunStep(t, p.Ctx, attach, b.Recorder, referenceRangeError(attachSQL, "42501", "must be owner of index pc_root_idx", fixture.Diagnostic{}))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 1, "actor"))
		run(fixture.Exec("RESET ROLE", 0))
		fixture.RunStep(t, p.Ctx, attach, b.Recorder, fixture.Exec(attachSQL, 0))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, partitionConstraintDuplicate(insert.SQL, "pc_a", "pc_a_idx", "Key (k, payload)=(1, actor) already exists.", 1, "actor"))
		run(fixture.Exec("DROP INDEX pc_root_idx", 0))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 1, "actor"))
		run(fixture.Select("SELECT k,payload FROM pc_root ORDER BY k,payload", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "actor"}, {int64(1), "actor"}}))
	})
}
