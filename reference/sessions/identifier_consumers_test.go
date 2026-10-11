package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIdentifierInsertQueryAccumulation(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(x INT)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE source_rows(x INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO source_rows VALUES(7)", 1))
	for _, q := range []string{"INSERT INTO t (SELECT)", "INSERT INTO t SELECT x FROM source_rows", "INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "INSERT INTO t (x) SELECT x FROM source_rows"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(q, 1)
			s.Prepared = prepared
			p.Step(t, s)
		}
	}
	p.Step(t, fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {nil}, {nil}}))
}

func TestReferenceIdentifierNormalizationDirect(t *testing.T) {
	for _, tc := range identifierNormalizationCases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE "+tc.spelling+" ("+tc.spelling+" INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO "+tc.spelling+" ("+tc.spelling+") VALUES (7)", 1))
			p.Step(t, fixture.Select("SELECT "+tc.spelling+" FROM "+tc.spelling+" ORDER BY "+tc.spelling, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
