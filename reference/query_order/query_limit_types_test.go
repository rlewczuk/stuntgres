package reference_test

import (
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitTypingProvider(t *testing.T) {
	p := referenceLimitFixture(t)
	p.Step(t, fixture.Exec("CREATE TABLE providers(n NUMERIC,f4 REAL,f8 DOUBLE PRECISION)", 0))
	p.Step(t, fixture.Exec("INSERT INTO providers VALUES(2.5,2.5,2.5),(NULL,NULL,NULL)", 2))
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
			step := fixture.Select("SELECT id FROM t ORDER BY id LIMIT "+tc.bound, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...))
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
	step := fixture.Select("SELECT id FROM t ORDER BY id FETCH FIRST CAST(2 AS BIGINT) ROWS ONLY", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2))
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
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
			step := fixture.Select(q, []string{"id"}, []string{"INT4"}, referenceLimitIDs(tc.ids...), tc.arg)
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
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
			step := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, args...)
			step.SelectRows = true
			step.ErrorStage = "execute"
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
}
