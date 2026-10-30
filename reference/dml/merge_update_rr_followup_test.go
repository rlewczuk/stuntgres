package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The next command sees the own replacement, never an extra snapshot predecessor.
func TestReferenceMergeUpdateRepeatableReadFollowup(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		count, own      int64
	}{
		{"unconditional", "", 1, 101},
		{"original10Only", " AND t.v=10", 0, 100},
		{"own100Only", " AND t.v=100", 1, 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				_, worker := fixture.LockConnections(t, p.Ctx, b.DB)
				run := fixture.MergeRun(t, p, b, false)
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
				fixture.LockExec(t, p.Ctx, worker, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
				fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=21 WHERE id=2", 1)
				fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=100 WHERE id=1", 1)
				rows := func(own int64) fixture.Step {
					return fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), own}, {int64(2), int64(21)}})
				}
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, rows(100))
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED" + tc.condition + " THEN UPDATE SET v=t.v+1"
				step := fixture.Exec(q, tc.count)
				step.Prepared = prepared
				if b.PG {
					step.Affected = 0
				}
				fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, rows(tc.own))
				fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=v+1 WHERE id=2", 1)
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
				fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}
