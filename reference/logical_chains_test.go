package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Long chains are analysis witnesses, not optimizer evaluation-order promises.
func TestReferenceLogicalLongChainsAndPositions(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT)", 0))
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1)", 1))
	for _, tc := range []struct {
		expr     string
		want     any
		retained bool
	}{
		{strings.Repeat("TRUE AND ", 256) + "NULL", nil, false},
		{strings.Repeat("FALSE OR ", 256) + "TRUE", true, true},
		{strings.Repeat("NOT ", 256) + "NULL", nil, false},
		{strings.Repeat("NOT ", 257) + "TRUE", false, false},
	} {
		step := referenceSelect("SELECT /*é*/ "+tc.expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}})
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
		want := [][]any{}
		if tc.retained {
			want = [][]any{{int64(1)}}
		}
		step = referenceSelect("SELECT id FROM t WHERE "+tc.expr, []string{"id"}, []string{"INT4"}, want)
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
	for _, tc := range []struct{ prefix, construct string }{
		{"SELECT /*é*/ " + strings.Repeat("TRUE AND ", 256) + "(", "AND"},
		{"SELECT /*é*/ " + strings.Repeat("FALSE OR ", 256) + "(", "OR"},
		{"SELECT /*é*/ " + strings.Repeat("NOT ", 256) + "(", "NOT"},
	} {
		step := referenceImplicitError(tc.prefix+"1) FROM t", referenceDiagnostic{Code: "42804", Message: "argument of " + tc.construct + " must be type boolean, not type integer", Position: strconv.Itoa(utf8.RuneCountInString(tc.prefix) + 1)})
		step.selectRows = true
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
	}
}
