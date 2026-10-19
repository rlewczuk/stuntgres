package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesQuery(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ input, want string }{
			{"fat & (rat | cat)", "'fat' & ( 'rat' | 'cat' )"},
			{"fat & rat & ! cat", "'fat' & 'rat' & !'cat'"},
			{"a:* & nbb:*ac | doo:a* | goo", "'a':* & 'nbb':*AC | 'doo':*A | 'goo'"},
			{"!!b", "!!'b'"}, {"a & a", "'a' & 'a'"},
			{"a <1> b", "'a' <-> 'b'"}, {"a <0> b", "'a' <0> 'b'"}, {"a <16384> b", "'a' <16384> 'b'"},
			{"a <-> (b <-> c)", "'a' <-> ( 'b' <-> 'c' )"},
			{"a <-> b <-> c", "'a' <-> 'b' <-> 'c'"},
			{"a | b & c <-> !d", "'a' | 'b' & 'c' <-> !'d'"},
			{"a:aaDdcb**", "'a':*ABCD"}, {"a:", "'a'"},
			{"'É space' & 'a''b' | 'c\\\\d'", "'É space' & 'a''b' | 'c\\\\d'"},
		} {
			t.Run(tc.input, func(t *testing.T) { ftsTypesScalar(t, p, b, "tsquery", tc.input, tc.want) })
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, op := range []string{" & ", " | "} {
			for _, operands := range []int{16384, 16385} {
				input := strings.Join(repeatFTSWord(operands, "a"), op)
				if b.PG {
					// The released server's recursive output hits its physical
					// stack, not a 32768-node codec bound. Native is iterative.
					d := fixture.ControlsReferenceDiagnostic("54001", "stack depth limit exceeded")
					d.Hint = "Increase the configuration parameter \"max_stack_depth\" (currently 2048kB), after ensuring the platform's stack depth limit is adequate."
					s := fixture.ErrorDiagnostic("SELECT $1::tsquery AS q", d, input)
					s.SelectRows = true
					s.ErrorStage = "execute"
					run(s)
				} else {
					ftsTypesScalar(t, p, b, "tsquery", input, strings.Join(repeatFTSWord(operands, "'a'"), op))
				}
			}
		}
		for _, depth := range []int{31, 32, 33, 256, 1024} {
			ftsTypesScalar(t, p, b, "tsquery", strings.Repeat("(", depth)+"a"+strings.Repeat(")", depth), "'a'")
			input := strings.Repeat("!", depth) + "a"
			if depth <= 32 {
				ftsTypesScalar(t, p, b, "tsquery", input, strings.Repeat("!", depth)+"'a'")
			} else {
				d := fixture.ControlsReferenceDiagnostic("XX000", "tsquery stack too small")
				d.Context = "unnamed portal parameter $1 = '...'"
				s := fixture.ErrorDiagnostic("SELECT $1::tsquery", d, input)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
			}
		}
		for _, op := range []string{" | ", " & ", " <-> "} {
			// A pending binary operator consumes one slot in the same
			// released 32-entry operator frame as the following NOT chain.
			ftsTypesScalar(t, p, b, "tsquery", "a"+op+strings.Repeat("!", 31)+"b", "'a'"+op+strings.Repeat("!", 31)+"'b'")
			d := fixture.ControlsReferenceDiagnostic("XX000", "tsquery stack too small")
			d.Context = "unnamed portal parameter $1 = '...'"
			s := fixture.ErrorDiagnostic("SELECT $1::tsquery", d, "a"+op+strings.Repeat("!", 32)+"b")
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			// Parentheses own a separate pending-operator frame.
			ftsTypesScalar(t, p, b, "tsquery", "a"+op+"("+strings.Repeat("!", 32)+"b)", "'a'"+op+strings.Repeat("!", 32)+"'b'")
		}
	})
}
