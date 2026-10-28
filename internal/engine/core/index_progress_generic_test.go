package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
)

func indexProgressGenericMethods(t *testing.T) {
	for _, method := range []string{"gist", "spgist"} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/overlay=%v", method, explicit), func(t *testing.T) {
				e := engine.New()
				worker := sessionStart(t, e, nil)
				sessionSQL(t, worker, "CREATE TABLE progress_build(k point)")
				sessionSQL(t, worker, "INSERT INTO progress_build VALUES('(1,2)'),('(3,4)'),(NULL),('(1,2)')")
				if explicit {
					sessionSQL(t, worker, "BEGIN")
				}
				b := indexProgressStage(t, e, worker, "CREATE INDEX progress_new ON progress_build USING "+method+"(k)")
				done := b.run()
				joined := false
				defer func() {
					close(b.gate.release)
					if !joined {
						b.op.Cancel(context.Canceled)
						<-done
					}
				}()
				scans, visits, finishes := 0, 0, 0
				for {
					select {
					case step := <-b.gate.steps:
						facts := indexProgressFacts(t, e, b.observer)
						if len(facts) != 1 {
							t.Fatal(facts)
						}
						f := facts[0]
						if f.Phase != "building index" || f.TuplesTotal != 0 || f.TuplesDone != 0 || f.BlocksTotal != 1 || f.Index != 0 || f.Relation != b.parent.ObjectID {
							t.Fatal(f)
						}
						switch step.kind {
						case "scan":
							scans++
							visits = 0
							if f.BlocksDone != 0 {
								t.Fatal(f)
							}
						case "visited":
							visits++
							want := int64(0)
							if step.n == 4 {
								want = 1
							}
							if step.n != visits || f.BlocksDone != want {
								t.Fatal(step, f)
							}
						case "sort":
							finishes++
							if step.n != 4 || f.BlocksDone != 1 {
								t.Fatal(step, f)
							}
						}
						close(step.ack)
					case err := <-done:
						joined = true
						if err != nil || scans == 0 || visits != 4 || finishes == 0 {
							t.Fatal(err, scans, visits, finishes)
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
}
