package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func partitionDetachSmokeState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	s := fixture.Select("SELECT k,payload FROM pd_parent ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}})
	s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{false, true})
	run(s)
	s = fixture.Select("SELECT * FROM pd_child ORDER BY k", []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"b", int64(2)}, {"d", int64(4)}})
	s.CheckMetadata = referenceRangeMetadata(pg, s.Columns, s.Types, []bool{true, false})
	run(s)
	run(partitionDetachClass("pd_child", false))
	run(partitionDetachTopEdges("pd_child_key", false))
	run(partitionConstraintEdge("pd_sibling_k_key", "pd_key", "pd_sibling"))
	run(partitionConstraintDuplicate("INSERT INTO pd_child(payload,k) VALUES('duplicate',1)", "pd_child", "pd_child_key", "Key (k)=(1) already exists."))
}

func TestReferencePartitionDetachFeatureSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { conn.Close() })
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				partitionDetachSetup(run, "pd_parent")
				parentQ, childQ := "SELECT k,payload FROM pd_parent ORDER BY k", "SELECT * FROM pd_child ORDER BY k"
				parent := partitionDetachHold(t, p, conn, parentQ)
				child := partitionDetachHold(t, p, conn, childQ)
				q := "ALTER TABLE pd_parent DETACH PARTITION pd_child"
				utility := partitionDetachHold(t, p, conn, q)
				fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.Select(parentQ, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(2), "b"}, {int64(3), "c"}}))
				run(fixture.Exec("BEGIN", 0))
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec(q, 0))
				run(referenceRangeRows("SELECT k FROM pd_parent", 3))
				run(fixture.Exec("ROLLBACK", 0))
				run(referenceRangeRows("SELECT k FROM pd_parent ORDER BY k", 1, 2, 3))
				run(partitionConstraintEdge("pd_child_key", "pd_key", "pd_child"))
				if prepared {
					fixture.RunStep(t, p.Ctx, utility, b.Recorder, fixture.Exec(q, 0))
				} else {
					run(fixture.Exec(q, 0))
				}
				fixture.RunStep(t, p.Ctx, parent, b.Recorder, fixture.Select(parentQ, []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "c"}}))
				fixture.RunStep(t, p.Ctx, child, b.Recorder, fixture.Select(childQ, []string{"payload", "k"}, []string{"TEXT", "INT4"}, [][]any{{"a", int64(1)}, {"b", int64(2)}}))
				run(fixture.Exec("INSERT INTO pd_child(payload,k) VALUES('d',4)", 1))
				partitionDetachSmokeState(t, p, conn, b.Recorder, b.PG)
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					partitionDetachSmokeState(t, p, conn, b.Recorder, false)
					if p.Postgres != nil {
						partitionAttachReplay(t, p, dump.String(), partitionDetachSmokeState)
					}
				}
				run(fixture.Exec("DROP TABLE pd_parent", 0))
				run(referenceRangeRows("SELECT k FROM pd_child ORDER BY k", 1, 2, 4))
			})
		})
	}
}
