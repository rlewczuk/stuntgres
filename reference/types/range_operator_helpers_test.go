package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations are independently derived from PG18 Tables 9.58–9.59.
func rangeOperatorScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

type operatorFamily struct{ name, element, one, two, three, seven, nine string }

var operatorFamilies = []operatorFamily{
	{"int4range", "integer", "1", "2", "3", "7", "9"},
	{"int8range", "bigint", "1", "2", "3", "7", "9"},
	{"numrange", "numeric", "1", "2", "3", "7", "9"},
	{"daterange", "date", "2000-01-01", "2000-01-02", "2000-01-03", "2000-01-07", "2000-01-09"},
	{"tsrange", "timestamp", "2000-01-01 00:00:00", "2000-01-02 00:00:00", "2000-01-03 00:00:00", "2000-01-07 00:00:00", "2000-01-09 00:00:00"},
	{"tstzrange", "timestamptz", "2000-01-01 00:00:00+00", "2000-01-02 00:00:00+00", "2000-01-03 00:00:00+00", "2000-01-07 00:00:00+00", "2000-01-09 00:00:00+00"},
}

func (f operatorFamily) span(lo, hi string) string { return "[" + lo + "," + hi + ")" }
func (f operatorFamily) multi() string             { return strings.Replace(f.name, "range", "multirange", 1) }
func operatorBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
func operatorBool(t *testing.T, p *fixture.Pair, b fixture.Backend, expr, relation string, want [][]any, args ...any) {
	t.Helper()
	s := fixture.Select("SELECT "+expr+" AS v FROM "+relation, []string{"v"}, []string{"BOOL"}, want, args...)
	operatorBoth(t, p, b, s)
}
func operatorValue(t *testing.T, p *fixture.Pair, b fixture.Backend, expr, relation, typ string, want [][]any) {
	t.Helper()
	operatorBoth(t, p, b, rangeSelect(b, "SELECT "+expr+" AS v FROM "+relation, []string{"v"}, []string{strings.ToUpper(typ)}, want, nil))
}
