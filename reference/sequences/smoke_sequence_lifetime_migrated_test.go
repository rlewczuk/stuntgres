package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeSequenceTemporaryPhysicalEndSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			s := fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, false)
			for _, q := range []string{"CREATE SEQUENCE s START 40", "CREATE TEMP SEQUENCE s START 7", "BEGIN", "COMMIT"} {
				s.Exec(s.C, q, 0)
			}
			s.Value(s.C, "SELECT nextval('s')", 7)
			s.Value(s.Peer, "SELECT nextval('s')", 40)
			s.Retire()
			fresh := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			d := fixture.TempReferenceDiagnostic("55000", `currval of sequence "s" is not yet defined in this session`, "", "")
			fixture.RunStep(t, p.Ctx, fresh, backend.Recorder, fixture.Step{Query: "SELECT currval('s')", SelectRows: true, State: d.Code, Diagnostic: d, ErrorStage: "execute"})
			s.Value(fresh, "SELECT nextval('s')", 41)
		})
	}
}
