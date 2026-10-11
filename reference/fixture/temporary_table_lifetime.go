package fixture

import (
	"testing"
)

func TemporaryTableScenario(t *testing.T, body func(*TempReferenceSession)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) { body(NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared)) })
			}
		})
	}
}

func TemporaryTableRows(s *TempReferenceSession, c Executor, name string, values ...int64) {
	s.T.Helper()
	rows := make([][]any, 0, len(values))
	for _, v := range values {
		rows = append(rows, []any{v})
	}
	RunStep(s.T, s.P.Ctx, c, s.Record, SequenceInt64Step(Step{Query: "SELECT v FROM " + name + " ORDER BY v", Prepared: s.Prepared, SelectRows: true, Columns: []string{"v"}, Types: []string{"INT4"}, Rows: rows}))
}
