//go:build reference

package reference_test

import (
	"fmt"
	"testing"
)

// CHAR/string is ambiguous; CHAR/nonstring can resolve a unique mixed overload.
// Expected fields and CHAR/nonstring results follow root's pinned18 oracle capture.
func stringCoreReferenceChar(t *testing.T, p *referencePair, oracle bool) {
	for _, v := range []any{"A", `\377`, `\200`, `\000`, nil} {
		for _, tc := range []struct{ q, message, position string }{
			{"SELECT '['::text||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: text || "char"`, "17"},
			{"SELECT CASE WHEN false THEN relkind ELSE $1 END||']'::text AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", `operator is not unique: "char" || text`, "48"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("ambiguity/%s/value=%v/prepared=%t", tc.position, v, prepared), func(t *testing.T) {
					diagnostic := referenceDiagnostic{Code: "42725", Message: tc.message, Position: tc.position, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
					s := referenceImplicitError(tc.q, diagnostic, v)
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					if oracle {
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
					} else {
						p.step(t, s)
					}
				})
			}
		}
		var forward, reverse any
		if v != nil {
			output := v.(string)
			if output == `\000` {
				output = ""
			}
			forward = output + "42"
			reverse = "42" + output
		}
		for _, tc := range []stringCoreReferenceCase{
			{"SELECT CASE WHEN false THEN relkind ELSE $1 END||42 AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", forward, "TEXT"},
			{"SELECT 42||CASE WHEN false THEN relkind ELSE $1 END AS v FROM pg_catalog.pg_class WHERE relname='string_core_seed'", reverse, "TEXT"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("unique/%s/value=%v/prepared=%t", tc.query, v, prepared), func(t *testing.T) {
					s := stringCoreSelect(tc, v)
					if prepared {
						s = referencePrepared(s)
					}
					if oracle {
						referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
					} else {
						p.step(t, s)
					}
				})
			}
		}
	}
}

func stringCoreOracleHangulBoundary(t *testing.T, p *referencePair) {
	// REL_18_0 inclusive TBASE recomposition consumes U+11A7. Predicate quickcheck
	// can return YES without recomposition; these are independent oracle probes,
	// not a universal normalize(input)==input implementation assumption.
	for _, tc := range []stringCoreReferenceCase{
		{"SELECT normalize('가ᆧ',NFC) AS v", "가", "TEXT"},
		{"SELECT normalize('가ᆧ',NFKC) AS v", "가", "TEXT"},
		{"SELECT normalize('가ᆧ',NFD) AS v", "가ᆧ", "TEXT"},
		{"SELECT normalize('가ᆧ',NFKD) AS v", "가ᆧ", "TEXT"},
		{"SELECT '가ᆧ' IS NFC NORMALIZED AS v", true, "BOOL"},
		{"SELECT '가ᆧ' IS NFKC NORMALIZED AS v", true, "BOOL"},
		{"SELECT '가ᆧ' IS NFC NORMALIZED AS v", false, "BOOL"},
		{"SELECT '가ᆧ' IS NFKC NORMALIZED AS v", false, "BOOL"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("hangul_boundary/%s/prepared=%t", tc.query, prepared), func(t *testing.T) {
				s := stringCoreSelect(tc)
				if prepared {
					s = referencePrepared(s)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
			})
		}
	}
}
