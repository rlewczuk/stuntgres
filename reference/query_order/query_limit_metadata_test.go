package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitMetadata(t *testing.T) {
	p := referenceLimitFixture(t)
	for _, tc := range []struct {
		suffix string
		rows   [][]any
	}{{"LIMIT 2", [][]any{{}, {}}}, {"LIMIT 0", [][]any{}}, {"OFFSET 99", [][]any{}}} {
		step := fixture.Select("SELECT FROM t "+tc.suffix, []string{}, []string{}, tc.rows)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
	for _, suffix := range []string{"LIMIT 0", "OFFSET 99"} {
		step := fixture.Select("SELECT id AS ident,v FROM t ORDER BY id "+suffix, []string{"ident", "v"}, []string{"INT4", "INT4"}, [][]any{})
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
