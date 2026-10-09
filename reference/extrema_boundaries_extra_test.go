package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceExtremaMaxCapabilities(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_cap(v INT)", 0))
		run(referenceExec("INSERT INTO extrema_cap VALUES(1)", 1))
		run(referenceExec("CREATE DOMAIN extrema_cap_names AS NAME[]", 0))
		for _, tc := range []struct {
			query, typ, message string
			value               any
		}{
			{"SELECT max('{z}'::name[]) FROM extrema_cap", "_NAME", "max for name[] is unsupported", []byte("{z}")},
			{"SELECT max(NULL::name[]) FROM extrema_cap", "_NAME", "max for name[] is unsupported", nil},
			{"SELECT max(NULL::extrema_cap_names) FROM extrema_cap", "_NAME", "max for name[] is unsupported", nil},
			{"SELECT max(NULL::pg_node_tree) FROM extrema_cap", "TEXT", "max for pg_node_tree is unsupported", nil},
		} {
			if pg {
				extremaResult(t, p, conn, recorder, pg, tc.query, []string{"max"}, []string{tc.typ}, [][]any{{tc.value}})
			} else {
				want := controlsReferenceDiagnostic("0A000", tc.message)
				want.Position = "8"
				extremaDiagnostic(t, p, conn, recorder, tc.query, want, "prepare")
			}
		}
	})
}

func TestReferenceExtremaBatchAtomicBoundary(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE extrema_batch(v INTEGER)", 0))
		const q = "INSERT INTO extrema_batch VALUES(99); SELECT min(TRUE) FROM extrema_batch"
		if pg {
			want := controlsReferenceDiagnostic("42883", "function min(boolean) does not exist")
			want.Position = referencePosition(strings.Index(q, "min(") + 1)
			want.Hint = extremaSignatureHint
			run(referenceErrorDiagnostic(q, want))
		} else {
			run(referenceError(q, "0A000"))
		}
		run(referenceSelect("SELECT v FROM extrema_batch", []string{"v"}, []string{"INT4"}, nil))
	})
}

func TestReferenceExtremaRecursiveNoFromBoundary(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE SEQUENCE extrema_no_from", 0))
		const q = "SELECT nextval('extrema_no_from'),min(1)+max(2)"
		if pg {
			run(referenceSelect(q, []string{"nextval", "?column?"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(3)}}))
		} else {
			run(referenceError(q, "0A000"))
		}
		want := int64(1)
		if pg {
			want = 2
		}
		run(referenceSelect("SELECT nextval('extrema_no_from')", []string{"nextval"}, []string{"INT8"}, [][]any{{want}}))
	})
}
