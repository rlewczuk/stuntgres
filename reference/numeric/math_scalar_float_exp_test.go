//go:build reference

package reference_test

import (
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarExpSubnormals(t *testing.T) {
	p := fixture.NewPair(t)
	// Unknown string arguments select FLOAT8 through the existing binder API,
	// so no PG-only cast syntax is needed for native compatibility witnesses.
	for _, tc := range []struct {
		input string
		bits  uint64
	}{
		{"-708.3964185322642", 0x000ffffffffffe7c},
		{"-709", 0x0008bfe55de02338},
		{"-744", 0x0000000000000002},
		{"-745", 0x0000000000000001},
		{"-745.1332191019411", 0x0000000000000001},
	} {
		t.Run(tc.input, func(t *testing.T) {
			s := fixture.Select("SELECT exp('"+tc.input+"')", []string{"exp"}, []string{"FLOAT8"}, [][]any{{math.Float64frombits(tc.bits)}})
			p.Step(t, s)
			p.Step(t, fixture.Prepared(s))
		})
	}
	s := fixture.ImplicitError("SELECT exp('-745.1332191019412')", fixture.Diagnostic{Code: "22003", Message: "value out of range: underflow"})
	s.ErrorStage = "execute"
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
}
