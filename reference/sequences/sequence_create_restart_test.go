package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

// Configured START and the first RESTART value are independently distinct.
func TestReferenceSequenceCreateRestart(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
					exec := func(q string) {
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Step{Query: q, Prepared: prepared})
					}
					exec("CREATE SEQUENCE sentinel START 41")
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('sentinel')", "nextval", 41)
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
						sequenceRestartState(t, p, c, backend.Recorder, backend.PG, tc.name, tc.initial, false)
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT currval('sentinel')", "currval", 41)
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT lastval()", "lastval", 41)
						want := fixture.ControlsReferenceDiagnostic("55000", `currval of sequence "`+tc.name+`" is not yet defined in this session`)
						var value int64
						err := c.QueryRowContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder), "SELECT currval('"+tc.name+"')").Scan(&value)
						fixture.AssertDiagnostic(t, err, &want)
						var oid int64
						if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", tc.name).Scan(&oid); err != nil || oid <= 0 {
							t.Fatal("sequence identity", oid, err)
						}
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Step{Query: "SELECT seqstart FROM pg_sequence WHERE seqrelid=$1::oid", Args: []any{oid}, Prepared: prepared, SelectRows: true, Columns: []string{"seqstart"}, Types: []string{"INT8"}, Rows: [][]any{{int64(5)}}})
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('"+tc.name+"')", "nextval", tc.initial)
						exec("ALTER SEQUENCE " + tc.name + " RESTART")
						sequenceRestartState(t, p, c, backend.Recorder, backend.PG, tc.name, 5, false)
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('"+tc.name+"')", "nextval", 5)
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('sentinel')", "nextval", 42)
						sequenceRestartValue(t, p, c, backend.Recorder, "SELECT setval('sentinel',41,true)", "setval", 41)
					}
				})
			}
		})
	}
}

func TestReferenceSequenceCreateRestartCacheAndHeld(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					c, b := fixture.LockConnections(t, p.Ctx, backend.DB)
					exec := func(q string) {
						fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Step{Query: q, Prepared: prepared})
					}
					exec("CREATE SEQUENCE cached START 5 RESTART WITH 9 CACHE 3")
					sequenceRestartState(t, p, c, backend.Recorder, backend.PG, "cached", 9, false)
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('cached')", "nextval", 9)
					sequenceRestartState(t, p, c, backend.Recorder, backend.PG, "cached", 11, true)
					// Original same-session cache tail precedes the peer's new reservation.
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('cached')", "nextval", 10)
					sequenceRestartState(t, p, c, backend.Recorder, backend.PG, "cached", 11, true)
					exec("CREATE SEQUENCE peer_cached START 5 RESTART WITH 9 CACHE 3")
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('peer_cached')", "nextval", 9)
					sequenceRestartValue(t, p, b, backend.Recorder, "SELECT nextval('peer_cached')", "nextval", 12)
					sequenceRestartState(t, p, c, backend.Recorder, backend.PG, "peer_cached", 14, true)
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT currval('peer_cached')", "currval", 9)
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT lastval()", "lastval", 9)
					sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('peer_cached')", "nextval", 10)
					if prepared {
						q := "CREATE SEQUENCE retained START 5 RESTART WITH 9"
						s, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer s.Close()
						held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
						for range 2 {
							fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Step{Query: q})
							sequenceRestartState(t, p, c, backend.Recorder, backend.PG, "retained", 9, false)
							sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('retained')", "nextval", 9)
							exec("ALTER SEQUENCE retained RESTART")
							sequenceRestartValue(t, p, c, backend.Recorder, "SELECT nextval('retained')", "nextval", 5)
							exec("DROP SEQUENCE retained")
						}
					}
				})
			}
		})
	}
}
