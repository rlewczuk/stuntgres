package reference_test

import (
	"context"
	"testing"
)

// Configured START and the first RESTART value are independently distinct.
func TestReferenceSequenceCreateRestart(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c := sequenceCoreConn(t, p.ctx, backend.db)
					exec := func(q string) {
						referenceRunStep(t, p.ctx, c, backend.recorder, referenceStep{query: q, prepared: prepared})
					}
					exec("CREATE SEQUENCE sentinel START 41")
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('sentinel')", "nextval", 41)
					for _, tc := range []struct {
						name, ddl string
						initial   int64
					}{
						{"bare", "CREATE SEQUENCE bare START 5 RESTART", 5},
						{"explicit", "CREATE SEQUENCE explicit START 5 RESTART 9", 9},
						{"with_value", "CREATE SEQUENCE with_value START 5 RESTART WITH 9", 9},
						{"temporary", "CREATE TEMP SEQUENCE temporary START 5 RESTART 9", 9},
						{"unlogged", "CREATE UNLOGGED SEQUENCE unlogged START 5 RESTART WITH 9", 9},
						{"absent", "CREATE SEQUENCE IF NOT EXISTS absent START 5 RESTART WITH 9", 9},
					} {
						exec(tc.ddl)
						sequenceRestartState(t, p, c, backend.recorder, backend.pg, tc.name, tc.initial, false)
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT currval('sentinel')", "currval", 41)
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT lastval()", "lastval", 41)
						want := controlsReferenceDiagnostic("55000", `currval of sequence "`+tc.name+`" is not yet defined in this session`)
						var value int64
						err := c.QueryRowContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder), "SELECT currval('"+tc.name+"')").Scan(&value)
						referenceAssertDiagnostic(t, err, &want)
						var oid int64
						if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname=$1", tc.name).Scan(&oid); err != nil || oid <= 0 {
							t.Fatal("sequence identity", oid, err)
						}
						referenceRunStep(t, p.ctx, c, backend.recorder, referenceStep{query: "SELECT seqstart FROM pg_sequence WHERE seqrelid=$1::oid", args: []any{oid}, prepared: prepared, selectRows: true, columns: []string{"seqstart"}, types: []string{"INT8"}, rows: [][]any{{int64(5)}}})
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('"+tc.name+"')", "nextval", tc.initial)
						exec("ALTER SEQUENCE " + tc.name + " RESTART")
						sequenceRestartState(t, p, c, backend.recorder, backend.pg, tc.name, 5, false)
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('"+tc.name+"')", "nextval", 5)
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('sentinel')", "nextval", 42)
						sequenceRestartValue(t, p, c, backend.recorder, "SELECT setval('sentinel',41,true)", "setval", 41)
					}
				})
			}
		})
	}
}

func TestReferenceSequenceCreateRestartCacheAndHeld(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					c, b := referenceLockConnections(t, p.ctx, backend.db)
					exec := func(q string) {
						referenceRunStep(t, p.ctx, c, backend.recorder, referenceStep{query: q, prepared: prepared})
					}
					exec("CREATE SEQUENCE cached START 5 RESTART WITH 9 CACHE 3")
					sequenceRestartState(t, p, c, backend.recorder, backend.pg, "cached", 9, false)
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('cached')", "nextval", 9)
					sequenceRestartState(t, p, c, backend.recorder, backend.pg, "cached", 11, true)
					// Original same-session cache tail precedes the peer's new reservation.
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('cached')", "nextval", 10)
					sequenceRestartState(t, p, c, backend.recorder, backend.pg, "cached", 11, true)
					exec("CREATE SEQUENCE peer_cached START 5 RESTART WITH 9 CACHE 3")
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('peer_cached')", "nextval", 9)
					sequenceRestartValue(t, p, b, backend.recorder, "SELECT nextval('peer_cached')", "nextval", 12)
					sequenceRestartState(t, p, c, backend.recorder, backend.pg, "peer_cached", 14, true)
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT currval('peer_cached')", "currval", 9)
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT lastval()", "lastval", 9)
					sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('peer_cached')", "nextval", 10)
					if prepared {
						q := "CREATE SEQUENCE retained START 5 RESTART WITH 9"
						s, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						held := referenceHeldStatement{referenceExecutor: c, statement: s, query: q}
						for range 2 {
							referenceRunStep(t, p.ctx, held, backend.recorder, referenceStep{query: q})
							sequenceRestartState(t, p, c, backend.recorder, backend.pg, "retained", 9, false)
							sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('retained')", "nextval", 9)
							exec("ALTER SEQUENCE retained RESTART")
							sequenceRestartValue(t, p, c, backend.recorder, "SELECT nextval('retained')", "nextval", 5)
							exec("DROP SEQUENCE retained")
						}
					}
				})
			}
		})
	}
}
