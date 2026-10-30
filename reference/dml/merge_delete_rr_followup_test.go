package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// An own replacement hides its predecessor even under a pinned RR snapshot.
// Deleting that replacement must not resurrect the snapshot's old version.
func TestReferenceMergeDeleteRepeatableReadFollowup(t *testing.T) {
	for _, tc := range []struct {
		name, condition string
		n               int64
		merge           bool
	}{
		{"unconditional", "", 1, false},
		{"original10Only", " AND t.v=10", 0, false},
		{"own100Only", " AND t.v=100", 1, false},
		{"mergeUnconditional", "", 1, true},
		{"mergeOriginal10Only", " AND t.v=10", 0, true},
		{"mergeOwn100Only", " AND t.v=100", 1, true},
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
				initial := fixture.Exec("UPDATE t SET v=100 WHERE id=1", 1)
				if tc.merge {
					initial = fixture.Exec("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=100", 1)
					if b.PG {
						initial.Affected = 0
					}
				}
				initial.Prepared = prepared
				fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, initial)
				original := fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(2), int64(21)}})
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, original)
				fixture.LockExec(t, p.Ctx, worker, "SAVEPOINT deletion", 0)
				q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED" + tc.condition + " THEN DELETE"
				step := fixture.Exec(q, tc.n)
				step.Prepared = prepared
				if b.PG {
					step.Affected = 0
				}
				fixture.InsertSelectRun(t, p.Ctx, worker, b.Recorder, step)
				if tc.n == 1 {
					fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT id,v FROM t ORDER BY id,v", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(21)}}))
				} else {
					fixture.RunStep(t, p.Ctx, worker, b.Recorder, original)
				}
				fixture.LockExec(t, p.Ctx, worker, "ROLLBACK TO deletion", 0)
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, original)
				fixture.LockExec(t, p.Ctx, worker, "RELEASE deletion", 0)
				fixture.LockExec(t, p.Ctx, worker, "UPDATE t SET v=v+1 WHERE id=2", 1)
				fixture.RunStep(t, p.Ctx, worker, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
				fixture.LockExec(t, p.Ctx, worker, "ROLLBACK", 0)
				run(fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
			})
		})
	}
}
