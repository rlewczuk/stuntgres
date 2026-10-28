package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
)

func indexProgressPartitionCallbacks(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, reuseTree := range []bool{false, true} {
			t.Run(fmt.Sprintf("overlay=%v/reuse_tree=%v", explicit, reuseTree), func(t *testing.T) {
				e := engine.New()
				worker := sessionStart(t, e, nil)
				for _, q := range []string{
					"CREATE TABLE progress_build(k INT) PARTITION BY RANGE(k)",
					"CREATE TABLE progress_lower PARTITION OF progress_build FOR VALUES FROM (0) TO (50) PARTITION BY RANGE(k)",
					"CREATE TABLE progress_first PARTITION OF progress_lower FOR VALUES FROM (0) TO (25)",
					"CREATE TABLE progress_second PARTITION OF progress_lower FOR VALUES FROM (25) TO (50)",
					"CREATE TABLE progress_upper PARTITION OF progress_build FOR VALUES FROM (50) TO (100)",
					"INSERT INTO progress_build VALUES(1),(30),(70)",
				} {
					sessionSQL(t, worker, q)
				}
				if reuseTree {
					sessionSQL(t, worker, "CREATE UNIQUE INDEX progress_reused ON progress_lower(k)")
				} else {
					sessionSQL(t, worker, "CREATE UNIQUE INDEX progress_reused ON progress_first(k)")
				}
				if explicit {
					sessionSQL(t, worker, "BEGIN")
				}
				b := indexProgressStage(t, e, worker, "CREATE UNIQUE INDEX progress_new ON progress_build(k)")
				p := core.ScopeIndexProgressEntry(b.scope)
				initial := core.IndexProgressSnapshot(p)
				wantInitial := int64(1)
				if initial.Relation != b.parent.ObjectID || initial.Index != 0 || initial.PartitionsTotal != 4 || initial.PartitionsDone != wantInitial {
					t.Fatal("staged leaves counted as built", initial, wantInitial)
				}
				done := b.run()
				joined := false
				defer func() {
					close(b.gate.release)
					if !joined {
						b.op.Cancel(context.Canceled)
						<-done
					}
				}()
				scans := 0
				previous := initial.PartitionsDone
				for {
					select {
					case step := <-b.gate.steps:
						facts := indexProgressFacts(t, e, b.observer)
						if len(facts) != 1 {
							t.Fatal(facts)
						}
						f := facts[0]
						if f.Relation != b.parent.ObjectID || f.Index != 0 || f.PartitionsTotal != 4 || f.PartitionsDone < previous || f.PartitionsDone > 4 {
							t.Fatal("recursive command identity/work", f, previous)
						}
						if step.kind == "scan" {
							scans++
						}
						previous = f.PartitionsDone
						close(step.ack)
					case err := <-done:
						joined = true
						if err != nil {
							t.Fatal(err)
						}
						f := core.IndexProgressSnapshot(p)
						wantDone := int64(4)
						if reuseTree {
							wantDone = 2
						}
						if scans == 0 || f.PartitionsDone != wantDone || f.PartitionsTotal != 4 {
							t.Fatal("actual recursive completion", scans, f)
						}
						if facts := indexProgressFacts(t, e, b.observer); len(facts) != 0 {
							t.Fatal(facts)
						}
						if explicit {
							sessionSQL(t, worker, "COMMIT")
						}
						return
					}
				}
			})
		}
	}
	t.Run("only", func(t *testing.T) {
		e := engine.New()
		worker := sessionStart(t, e, nil)
		sessionSQL(t, worker, "CREATE TABLE progress_build(k INT) PARTITION BY RANGE(k)")
		sessionSQL(t, worker, "CREATE TABLE progress_child PARTITION OF progress_build FOR VALUES FROM (0) TO (10)")
		b := indexProgressStage(t, e, worker, "CREATE UNIQUE INDEX progress_new ON ONLY progress_build(k)")
		p := core.ScopeIndexProgressEntry(b.scope)
		close(b.gate.release)
		if err := <-b.run(); err != nil {
			t.Fatal(err)
		}
		if f := core.IndexProgressSnapshot(p); f.PartitionsTotal != 0 || f.PartitionsDone != 0 {
			t.Fatal(f)
		}
	})
}
