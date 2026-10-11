package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Long chains are analysis witnesses, not optimizer evaluation-order promises.
func TestReferenceLogicalLongChainsAndPositions(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE t(id INT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO t VALUES(1)", 1))
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
		step := fixture.Select("SELECT /*é*/ "+tc.expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}})
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
		want := [][]any{}
		if tc.retained {
			want = [][]any{{int64(1)}}
		}
		step = fixture.Select("SELECT id FROM t WHERE "+tc.expr, []string{"id"}, []string{"INT4"}, want)
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	for _, tc := range []struct{ prefix, construct string }{
		{"SELECT /*é*/ " + strings.Repeat("TRUE AND ", 256) + "(", "AND"},
		{"SELECT /*é*/ " + strings.Repeat("FALSE OR ", 256) + "(", "OR"},
		{"SELECT /*é*/ " + strings.Repeat("NOT ", 256) + "(", "NOT"},
	} {
		step := fixture.ImplicitError(tc.prefix+"1) FROM t", fixture.Diagnostic{Code: "42804", Message: "argument of " + tc.construct + " must be type boolean, not type integer", Position: strconv.Itoa(utf8.RuneCountInString(tc.prefix) + 1)})
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}
