package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			schema := fmt.Sprintf("sequence_delete_%d", time.Now().UnixNano())
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						_, _ = c.ExecContext(p.Ctx, "ROLLBACK")
						_, _ = c.ExecContext(p.Ctx, "SET search_path TO public")
						if _, err := c.ExecContext(p.Ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					step := func(s fixture.Step) {
						t.Helper()
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
					}
					step(fixture.Exec("CREATE SCHEMA "+schema, 0))
					step(fixture.Exec("SET search_path TO "+schema, 0))
					step(fixture.Exec("CREATE SEQUENCE s", 0))
					step(fixture.Exec("CREATE TABLE t(v INT)", 0))
					step(fixture.Exec("INSERT INTO t VALUES(1),(1),(2)", 3))
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}})))
					snapshotRecord := &fixture.Recorder{RawBackend: backend.Recorder.RawBackend}
					snapshotQuery := "SELECT v FROM t ORDER BY v"
					held, err := c.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, snapshotRecord), snapshotQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					snapshot := fixture.Select(snapshotQuery, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
					consume := func() {
						fixture.RunStep(t, p.Ctx, fixture.SequenceFrozenRows{Executor: c, Rows: held, Query: snapshotQuery}, snapshotRecord, snapshot)
					}
					if backend.PG {
						consume()
					}
					for _, s := range []fixture.Step{
						fixture.Exec("BEGIN", 0), fixture.Exec("ALTER SEQUENCE s RESTART 100", 0),
						fixture.Prepared(fixture.Exec("DELETE FROM t WHERE v=1", 2)), fixture.Exec("INSERT INTO t VALUES(3)", 1),
						fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}), fixture.Exec("ROLLBACK", 0),
						fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}),
					} {
						step(s)
					}
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}})))
					for _, s := range []fixture.Step{
						fixture.Exec("BEGIN", 0), fixture.Exec("ALTER SEQUENCE s RESTART 200", 0), fixture.Exec("DELETE FROM t WHERE v=1", 2),
						fixture.Exec("INSERT INTO t VALUES(4)", 1), fixture.Exec("INSERT INTO t VALUES(5)", 1), fixture.Exec("DELETE FROM t WHERE v=5", 1), fixture.Exec("COMMIT", 0),
						fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}),
					} {
						step(s)
					}
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(200)}})))
					if !backend.PG {
						consume()
					}
					for _, s := range []fixture.Step{
						fixture.Exec("BEGIN", 0), fixture.Prepared(fixture.Exec("DELETE FROM t WHERE nextval('s')>0", 2)), fixture.Exec("ROLLBACK", 0),
						fixture.Select("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}),
					} {
						step(s)
					}
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(202)}})))
					wrong := fixture.ControlsReferenceDiagnostic("42809", `cannot change sequence "s"`)
					failure := fixture.Prepared(fixture.ErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", wrong))
					failure.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, failure)
					fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(202)}})))
					step(fixture.Exec("CREATE TABLE dst(v INT)", 0))
					step(fixture.Prepared(fixture.Exec("INSERT INTO dst SELECT v FROM t ORDER BY v", 2)))
					step(fixture.Select("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}))
				})
			}

		})
	}
}
