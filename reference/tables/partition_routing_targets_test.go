package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionRoutingTargets(t *testing.T) {
	t.Run("default_and_leaf", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pr_target(k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_target_low PARTITION OF pr_target FOR VALUES FROM(1) TO(3)", 0))
			run(fixture.Exec("CREATE TABLE pr_target_high PARTITION OF pr_target FOR VALUES FROM(3) TO(5)", 0))
			run(fixture.Exec("CREATE TABLE pr_target_default PARTITION OF pr_target DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pr_target VALUES(2)", 1))
			run(referenceRangeLeafMiss("UPDATE pr_target_low SET k=3", "pr_target_low", "3"))
			run(fixture.Prepared(referenceRangeLeafMiss("INSERT INTO pr_target_low VALUES(3)", "pr_target_low", "3")))
			run(referenceRangeRows("SELECT k FROM pr_target_low", 2))
			run(fixture.Exec("UPDATE pr_target SET k=8", 1))
			run(referenceRangeRows("SELECT k FROM pr_target_low"))
			run(referenceRangeRows("SELECT k FROM pr_target_default", 8))
			run(fixture.Prepared(referenceRangeLeafMiss("UPDATE pr_target_default SET k=3", "pr_target_default", "3")))
			run(referenceRangeLeafMiss("INSERT INTO pr_target_default VALUES(3)", "pr_target_default", "3"))
			run(referenceRangeRows("SELECT k FROM pr_target_default", 8))
			run(fixture.Exec("UPDATE pr_target SET k=3", 1))
			run(referenceRangeRows("SELECT k FROM pr_target_default"))
			run(referenceRangeRows("SELECT k FROM pr_target_high", 3))
			run(fixture.Exec("INSERT INTO pr_target VALUES(NULL)", 1))
			run(fixture.Select("SELECT k FROM pr_target_default", []string{"k"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Exec("UPDATE ONLY pr_target_high SET k=4", 1))
			run(referenceRangeLeafMiss("UPDATE ONLY pr_target_high SET k=5", "pr_target_high", "5"))
			run(referenceRangeRows("SELECT k FROM pr_target_high", 4))
		})
	})
	t.Run("subtree", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pr_tree(k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_mid PARTITION OF pr_tree FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_mid_low PARTITION OF pr_mid FOR VALUES FROM(0) TO(5)", 0))
			run(fixture.Exec("CREATE TABLE pr_mid_high PARTITION OF pr_mid FOR VALUES FROM(5) TO(10)", 0))
			run(fixture.Exec("CREATE TABLE pr_other PARTITION OF pr_tree FOR VALUES FROM(10) TO(20)", 0))
			run(fixture.Exec("INSERT INTO pr_tree VALUES(4)", 1))
			run(fixture.Prepared(fixture.Exec("UPDATE pr_mid SET k=6", 1)))
			run(referenceRangeRows("SELECT k FROM pr_mid_low"))
			run(referenceRangeRows("SELECT k FROM pr_mid_high", 6))
			run(referenceRangeLeafMiss("UPDATE pr_mid SET k=11", "pr_mid", "11"))
			run(referenceRangeRows("SELECT k FROM pr_tree", 6))
			run(fixture.Exec("UPDATE ONLY pr_mid SET k=7", 0))
			run(fixture.Exec("UPDATE ONLY pr_tree SET k=7", 0))
			run(fixture.Exec("UPDATE pr_tree SET k=11", 1))
			run(referenceRangeRows("SELECT k FROM pr_mid"))
			run(referenceRangeRows("SELECT k FROM pr_other", 11))
			run(fixture.Exec("CREATE TABLE pr_empty(k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Prepared(fixture.Exec("UPDATE pr_empty SET k=1", 0)))
			run(referenceRangeRows("SELECT k FROM pr_empty"))
			run(fixture.Exec("CREATE TABLE pr_nested(k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_nested_mid PARTITION OF pr_nested FOR VALUES FROM(0) TO(10) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_nested_low PARTITION OF pr_nested_mid FOR VALUES FROM(0) TO(3)", 0))
			run(fixture.Exec("INSERT INTO pr_nested VALUES(2)", 1))
			run(referenceRangeMiss("UPDATE pr_nested SET k=5", "pr_nested_mid", "k", "5"))
			run(referenceRangeRows("SELECT k FROM pr_nested_low", 2))
		})
	})
	t.Run("quoted_key", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec(`CREATE TABLE pr_quoted("Key Space" INT) PARTITION BY RANGE("Key Space")`, 0))
			run(fixture.Exec("CREATE TABLE pr_quoted_low PARTITION OF pr_quoted FOR VALUES FROM(1) TO(3)", 0))
			run(fixture.Exec("INSERT INTO pr_quoted VALUES(2)", 1))
			run(fixture.Prepared(referenceRangeMiss(`UPDATE pr_quoted SET "Key Space"=5`, "pr_quoted", `"Key Space"`, "5")))
			run(fixture.Select(`SELECT "Key Space" FROM pr_quoted`, []string{"Key Space"}, []string{"INT4"}, [][]any{{int64(2)}}))
		})
	})
	t.Run("hidden_detail", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"pr_actor"})
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE pr_actor", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pr_actor", 0))
			run(fixture.Exec("CREATE TABLE pr_private(id INT,k INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_private_low PARTITION OF pr_private FOR VALUES FROM(1) TO(3)", 0))
			run(fixture.Exec("CREATE TABLE pr_private_high PARTITION OF pr_private FOR VALUES FROM(3) TO(5)", 0))
			run(fixture.Exec("INSERT INTO pr_private VALUES(10,2)", 1))
			run(fixture.Exec("GRANT UPDATE(k),SELECT(id) ON pr_private TO pr_actor", 0))
			run(fixture.Exec("SET ROLE pr_actor", 0))
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			run(referenceRangeError("UPDATE pr_private SET k=5 WHERE id=10", "23514", `no partition of relation "pr_private" found for row`, fixture.Diagnostic{Schema: "public", Table: "pr_private"}))
			// The parent grant suffices for relocation; no leaf INSERT/DELETE grants.
			run(fixture.Prepared(fixture.Exec("UPDATE pr_private SET k=3 WHERE id=10", 1)))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Select("SELECT id,k FROM pr_private_high", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(3)}}))
			run(fixture.Select("SELECT id,k FROM pr_private_low", []string{"id", "k"}, []string{"INT4", "INT4"}, nil))
		})
	})
}
