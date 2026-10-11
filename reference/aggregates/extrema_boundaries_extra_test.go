package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtremaMaxCapabilities(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_cap(v INT)", 0))
		run(fixture.Exec("INSERT INTO extrema_cap VALUES(1)", 1))
		run(fixture.Exec("CREATE DOMAIN extrema_cap_names AS NAME[]", 0))
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
				want := fixture.ControlsReferenceDiagnostic("0A000", tc.message)
				want.Position = "8"
				extremaDiagnostic(t, p, conn, recorder, tc.query, want, "prepare")
			}
		}
	})
}

func TestReferenceExtremaBatchAtomicBoundary(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE TABLE extrema_batch(v INTEGER)", 0))
		const q = "INSERT INTO extrema_batch VALUES(99); SELECT min(TRUE) FROM extrema_batch"
		if pg {
			want := fixture.ControlsReferenceDiagnostic("42883", "function min(boolean) does not exist")
			want.Position = fixture.Position(strings.Index(q, "min(") + 1)
			want.Hint = extremaSignatureHint
			run(fixture.ErrorDiagnostic(q, want))
		} else {
			run(fixture.Error(q, "0A000"))
		}
		run(fixture.Select("SELECT v FROM extrema_batch", []string{"v"}, []string{"INT4"}, nil))
	})
}

func TestReferenceExtremaRecursiveNoFromBoundary(t *testing.T) {
	extremaReference(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
		run(fixture.Exec("CREATE SEQUENCE extrema_no_from", 0))
		const q = "SELECT nextval('extrema_no_from'),min(1)+max(2)"
		run(fixture.Select(q, []string{"nextval", "?column?"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(3)}}))
		run(fixture.Select("SELECT nextval('extrema_no_from')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
		run(fixture.Prepared(fixture.Select(q, []string{"nextval", "?column?"}, []string{"INT8", "INT4"}, [][]any{{int64(3), int64(3)}})))
		run(fixture.Select("SELECT nextval('extrema_no_from')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
}
