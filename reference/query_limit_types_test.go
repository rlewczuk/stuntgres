package reference_test

import (
	"math"
	"testing"
)

func TestReferenceQueryLimitTypingProvider(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE providers(n NUMERIC,f4 REAL,f8 DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO providers VALUES(2.5,2.5,2.5),(NULL,NULL,NULL)", 2))
	for _, tc := range []struct {
		bound string
		ids   []int64
	}{
		{"CAST(2 AS INTEGER)", []int64{1, 2}},
		{"CAST(2 AS BIGINT)", []int64{1, 2}},
		{"CAST(2 AS OID)", []int64{1, 2}},
		{"2.5", []int64{1, 2, 3}},
	} {
		t.Run(tc.bound, func(t *testing.T) {
			step := referenceSelect("SELECT id FROM t ORDER BY id LIMIT "+tc.bound, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...))
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	step := referenceSelect("SELECT id FROM t ORDER BY id FETCH FIRST CAST(2 AS BIGINT) ROWS ONLY", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2))
	p.step(t, step)
	p.step(t, referencePrepared(step))
	for _, tc := range []struct {
		column string
		arg    any
		ids    []int64
	}{
		{"n", "2.5", []int64{1, 2, 3}},
		{"f4", float32(2.5), []int64{1, 2}},
		{"f8", float64(2.5), []int64{1, 2}},
		{"n", nil, []int64{1, 2, 3, 4}},
		{"f4", nil, []int64{1, 2, 3, 4}},
		{"f8", nil, []int64{1, 2, 3, 4}},
	} {
		t.Run(tc.column+map[bool]string{false: "/value", true: "/null"}[tc.arg == nil], func(t *testing.T) {
			q := "SELECT t.id FROM t,providers WHERE providers." + tc.column + " IS NOT DISTINCT FROM $1 ORDER BY t.id LIMIT $1"
			step := referenceSelect(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...), tc.arg)
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	for _, tc := range []struct {
		column        string
		arg           any
		code, message string
	}{
		{"n", "NaN", "0A000", "cannot convert NaN to bigint"},
		{"n", "Infinity", "0A000", "cannot convert infinity to bigint"},
		{"n", "-Infinity", "0A000", "cannot convert infinity to bigint"},
		{"f4", math.NaN(), "22003", "bigint out of range"},
		{"f8", math.Inf(1), "22003", "bigint out of range"},
		{"f8", math.Inf(-1), "22003", "bigint out of range"},
		{"f8", float64(9223372036854775807), "22003", "bigint out of range"},
		{"", nil, "22003", "bigint out of range"},
	} {
		t.Run(tc.column+"/"+tc.message, func(t *testing.T) {
			q := "SELECT id FROM t LIMIT 9223372036854775808"
			var args []any
			if tc.column != "" {
				q = "SELECT t.id FROM t,providers WHERE providers." + tc.column + "=$1 LIMIT $1"
				args = []any{tc.arg}
			}
			step := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message}, args...)
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
}
