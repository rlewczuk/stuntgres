//go:build reference

package reference_test

import (
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

// PG-only syntax never reaches the native backend. Expected finite bits were
// independently rounded from a 180-digit decimal exponential of the exact
// represented binary64 input, not computed with Go's math.Exp.
// Root observed that the pinned image accepts these nonzero subnormals; only
// zero-result finite underflow is an error. Contradictions print actual records.
func TestReferenceMathScalarExpOracle(t *testing.T) {
	p := fixture.NewPair(t)
	for _, tc := range []struct {
		input, text, message string
		bits                 uint64
	}{
		{"-708", "3.307553003638408e-308", "", 0x0017c8ab2288c9ab},
		{"-708.396418532264", "2.2250738585075156e-308", "", 0x001000000000027c},
		{"-708.3964185322641", "2.2250738585072626e-308", "", 0x001000000000007c},
		{"-708.3964185322642", "2.2250738585070097e-308", "", 0x000ffffffffffe7c},
		{"-709", "1.216780750623423e-308", "", 0x0008bfe55de02338},
		{"-744", "1e-323", "", 0x0000000000000002},
		{"-745", "5e-324", "", 0x0000000000000001},
		{"-745.1332191019411", "5e-324", "", 0x0000000000000001},
		{"-745.1332191019412", "", "value out of range: underflow", 0},
		{"-746", "", "value out of range: underflow", 0},
		{"709", "8.218407461554972e+307", "", 0x7fdd422d2be5dc9b},
		{"710", "", "value out of range: overflow", 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			query := "SELECT exp('" + tc.input + "'::float8) AS result"
			for _, prepared := range []bool{false, true} {
				if tc.message != "" {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: tc.message})
					s.ErrorStage = "execute"
					s.SelectRows = true
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
					continue
				}
				want := math.Float64frombits(tc.bits)
				s := fixture.Select(query, []string{"result"}, []string{"FLOAT8"}, [][]any{{want}})
				s.Prepared = prepared
				got := fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
				value := got.Rows[0][0].(float64)
				if math.Float64bits(value) != tc.bits {
					t.Fatalf("input=%s prepared=%v actual=%g bits=%016x want=%016x", tc.input, prepared, value, math.Float64bits(value), tc.bits)
				}
				textQuery := "SELECT exp('" + tc.input + "'::float8)::text AS result"
				text := fixture.Select(textQuery, []string{"result"}, []string{"TEXT"}, [][]any{{tc.text}})
				text.Prepared = prepared
				fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, text)
			}
		})
	}
}
