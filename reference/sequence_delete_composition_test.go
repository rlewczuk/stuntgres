package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestReferenceSequenceDeleteComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			schema := fmt.Sprintf("sequence_delete_%d", time.Now().UnixNano())
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					defer func() {
						_, _ = c.ExecContext(p.ctx, "ROLLBACK")
						_, _ = c.ExecContext(p.ctx, "SET search_path TO public")
						if _, err := c.ExecContext(p.ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					step := func(s referenceStep) {
						t.Helper()
						s.prepared = prepared
						referenceRunStep(t, p.ctx, c, backend.recorder, s)
					}
					step(referenceExec("CREATE SCHEMA "+schema, 0))
					step(referenceExec("SET search_path TO "+schema, 0))
					step(referenceExec("CREATE SEQUENCE s", 0))
					step(referenceExec("CREATE TABLE t(v INT)", 0))
					step(referenceExec("INSERT INTO t VALUES(1),(1),(2)", 3))
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}})))
					snapshotRecord := &referenceRecorder{rawBackend: backend.recorder.rawBackend}
					snapshotQuery := "SELECT v FROM t ORDER BY v"
					held, err := c.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, snapshotRecord), snapshotQuery)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					snapshot := referenceSelect(snapshotQuery, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}})
					consume := func() {
						referenceRunStep(t, p.ctx, sequenceFrozenRows{referenceExecutor: c, rows: held, query: snapshotQuery}, snapshotRecord, snapshot)
					}
					if backend.pg {
						consume()
					}
					for _, s := range []referenceStep{
						referenceExec("BEGIN", 0), referenceExec("ALTER SEQUENCE s RESTART 100", 0),
						referencePrepared(referenceExec("DELETE FROM t WHERE v=1", 2)), referenceExec("INSERT INTO t VALUES(3)", 1),
						referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}), referenceExec("ROLLBACK", 0),
						referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}}),
					} {
						step(s)
					}
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}})))
					for _, s := range []referenceStep{
						referenceExec("BEGIN", 0), referenceExec("ALTER SEQUENCE s RESTART 200", 0), referenceExec("DELETE FROM t WHERE v=1", 2),
						referenceExec("INSERT INTO t VALUES(4)", 1), referenceExec("INSERT INTO t VALUES(5)", 1), referenceExec("DELETE FROM t WHERE v=5", 1), referenceExec("COMMIT", 0),
						referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}),
					} {
						step(s)
					}
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(200)}})))
					if !backend.pg {
						consume()
					}
					for _, s := range []referenceStep{
						referenceExec("BEGIN", 0), referencePrepared(referenceExec("DELETE FROM t WHERE nextval('s')>0", 2)), referenceExec("ROLLBACK", 0),
						referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}),
					} {
						step(s)
					}
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(202)}})))
					wrong := controlsReferenceDiagnostic("42809", `cannot change sequence "s"`)
					failure := referencePrepared(referenceErrorDiagnostic("DELETE FROM s WHERE nextval('s')>0", wrong))
					failure.errorStage = "execute"
					referenceRunStep(t, p.ctx, c, backend.recorder, failure)
					referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(202)}})))
					step(referenceExec("CREATE TABLE dst(v INT)", 0))
					step(referencePrepared(referenceExec("INSERT INTO dst SELECT v FROM t ORDER BY v", 2)))
					step(referenceSelect("SELECT v FROM dst ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(4)}}))
				})
			}

		})
	}
}
