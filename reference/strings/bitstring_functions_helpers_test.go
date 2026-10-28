package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func bitStringFunctionsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func bitStringFunctionsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func bitStringFunctionsError(q, code, message string, position int, prepared, analysis bool, args ...any) fixture.Step {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Position = fixture.Position(position)
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
	if prepared && analysis {
		s.ErrorStage = "prepare"
	}
	return s
}

const bitStringFunctionsManual = "SELECT bit_count(B'10111') AS count,bit_length(B'10111') AS bits,length(B'10111') AS len,octet_length(B'1011111011') AS octets,overlay(B'01010101010101010' PLACING B'11111' FROM 2 FOR 3) AS ov,position(B'010' IN B'000001101011') AS pos,substring(B'110010111111' FROM 3 FOR 2) AS sub"

func bitStringFunctionsManualStep() fixture.Step {
	return fixture.Select(bitStringFunctionsManual, []string{"count", "bits", "len", "octets", "ov", "pos", "sub"}, []string{"INT8", "INT4", "INT4", "INT4", "BIT", "INT4", "BIT"}, [][]any{{int64(4), int64(5), int64(5), int64(2), "0111110101010101010", int64(8), "00"}})
}
