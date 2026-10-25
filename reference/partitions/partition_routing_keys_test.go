package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func referencePartitionRoutingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func TestReferencePartitionRoutingKeys(t *testing.T) {
	t.Run("range", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pr_multi(id INT,a INT,b INT) PARTITION BY RANGE(a,b)", 0))
			run(fixture.Exec("CREATE TABLE pr_multi_in PARTITION OF pr_multi FOR VALUES FROM(1,5) TO(3,0)", 0))
			run(fixture.Exec("CREATE TABLE pr_multi_default PARTITION OF pr_multi DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pr_multi VALUES(1,1,5),(2,2,-100),(3,2,100),(4,1,4),(5,3,0),(6,NULL,5),(7,2,NULL)", 7))
			run(fixture.Select("SELECT id FROM pr_multi_in ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
			run(fixture.Select("SELECT id FROM pr_multi_default ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}}))
			run(fixture.Prepared(fixture.Exec("UPDATE pr_multi SET a=1,b=5 WHERE id=4", 1)))
			run(fixture.Exec("UPDATE pr_multi SET a=3,b=0 WHERE id=1", 1))
			run(fixture.Exec("UPDATE pr_multi SET b=NULL WHERE id=2", 1))
			run(fixture.Select("SELECT id FROM pr_multi_in ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(4)}}))
			run(fixture.Select("SELECT id FROM pr_multi_default ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(5)}, {int64(6)}, {int64(7)}}))
		})
	})
	t.Run("list", func(t *testing.T) {
		referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pr_list(k INT) PARTITION BY LIST(k)", 0))
			run(fixture.Exec("CREATE TABLE pr_list_one PARTITION OF pr_list FOR VALUES IN(1)", 0))
			run(fixture.Exec("CREATE TABLE pr_list_null PARTITION OF pr_list FOR VALUES IN(NULL)", 0))
			run(fixture.Exec("CREATE TABLE pr_list_default PARTITION OF pr_list DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pr_list VALUES(1)", 1))
			run(fixture.Exec("UPDATE pr_list SET k=7", 1))
			run(referenceRangeRows("SELECT k FROM pr_list_one"))
			run(referenceRangeRows("SELECT k FROM pr_list_default", 7))
			run(fixture.Prepared(fixture.Exec("UPDATE pr_list SET k=NULL", 1)))
			run(referenceRangeRows("SELECT k FROM pr_list_default"))
			run(fixture.Select("SELECT k FROM pr_list_null", []string{"k"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Exec("UPDATE pr_list SET k=1", 1))
			run(referenceRangeRows("SELECT k FROM pr_list_null"))
			run(referenceRangeRows("SELECT k FROM pr_list_one", 1))
		})
	})
	for _, missing := range []bool{false, true} {
		name := "hash"
		if missing {
			name = "hash_missing"
		}
		t.Run(name, func(t *testing.T) {
			referencePartitionRoutingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pr_hash(k INT) PARTITION BY HASH(k)", 0))
				run(fixture.Exec("CREATE TABLE pr_hash_0 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 4,REMAINDER 0)", 0))
				run(fixture.Exec("CREATE TABLE pr_hash_1 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 4,REMAINDER 1)", 0))
				if !missing {
					run(fixture.Exec("CREATE TABLE pr_hash_2 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 4,REMAINDER 2)", 0))
				}
				run(fixture.Exec("CREATE TABLE pr_hash_3 PARTITION OF pr_hash FOR VALUES WITH(MODULUS 4,REMAINDER 3)", 0))
				run(fixture.Exec("INSERT INTO pr_hash VALUES(1)", 1))
				if missing {
					s := referenceRangeMiss("UPDATE pr_hash SET k=$1", "pr_hash", "k", "2")
					s.Args = []any{2}
					run(fixture.Prepared(s))
					run(referenceRangeRows("SELECT k FROM pr_hash_0", 1))
					return
				}
				run(fixture.Exec("INSERT INTO pr_hash VALUES(NULL)", 1))
				run(fixture.Exec("UPDATE pr_hash SET k=2 WHERE k=1", 1))
				run(fixture.Prepared(fixture.Exec("UPDATE pr_hash SET k=-1 WHERE k IS NULL", 1)))
				run(referenceRangeRows("SELECT k FROM pr_hash_0"))
				run(referenceRangeRows("SELECT k FROM pr_hash_1", -1))
				run(referenceRangeRows("SELECT k FROM pr_hash_2", 2))
				run(referenceRangeRows("SELECT k FROM pr_hash_3"))
				run(referenceRangeRows("SELECT k FROM pr_hash ORDER BY k", -1, 2))
			})
		})
	}
}
