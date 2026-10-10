package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBinaryConversionNameImports(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			q, typ      string
			first, want any
		}{
			{"SELECT convert_from($1,$2) AS v", "TEXT", []byte{0xe9}, "é"},
			{"SELECT convert_to($1,$2) AS v", "BYTEA", "é", "\xe9"},
			{"SELECT convert($1,$2,'UTF8') AS v", "BYTEA", []byte{0xe9}, "é"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				valid := referenceSelect(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}, tc.first, "LATIN1")
				for _, name := range []string{"L\x00", "L\xff"} {
					bytes := "0x00"
					if name[1] == 0xff {
						bytes = "0xff"
					}
					d := controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": `+bytes)
					d.Context = "unnamed portal parameter $2"
					bad := referenceErrorDiagnostic(tc.q, d, tc.first, name)
					bad.selectRows = true
					bad.errorStage = "execute"
					stringEncodingFailure(t, p, b, c, tc.q, d, false, tc.first, name)
					binaryConversionHeld(t, p, b, c, tc.q, []referenceStep{valid, bad, valid})
				}
			})
		}
		// A raw Go string in the BYTEA role is literal payload, not SQL hex syntax.
		binaryConversionBoth(t, p, b, c, referenceSelect("SELECT encode($1,'hex') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"5c786539"}}, `\xe9`))
		binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert($1,'LATIN1','UTF8') AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{"é"}}, "\xe9"))
	})
}
