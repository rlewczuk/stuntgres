package reference_test

import "testing"

func TestReferenceIdentifierInsertQueryAccumulation(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(x INT)", 0))
	p.step(t, referenceExec("CREATE TABLE source_rows(x INT)", 0))
	p.step(t, referenceExec("INSERT INTO source_rows VALUES(7)", 1))
	for _, q := range []string{"INSERT INTO t (SELECT)", "INSERT INTO t SELECT x FROM source_rows", "INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "INSERT INTO t (x) SELECT x FROM source_rows"} {
		for _, prepared := range []bool{false, true} {
			s := referenceExec(q, 1)
			s.prepared = prepared
			p.step(t, s)
		}
	}
	p.step(t, referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {int64(7)}, {nil}, {nil}}))
}

func TestReferenceIdentifierNormalizationDirect(t *testing.T) {
	for _, tc := range identifierNormalizationCases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE "+tc.spelling+" ("+tc.spelling+" INT)", 0))
			p.step(t, referenceExec("INSERT INTO "+tc.spelling+" ("+tc.spelling+") VALUES (7)", 1))
			p.step(t, referenceSelect("SELECT "+tc.spelling+" FROM "+tc.spelling+" ORDER BY "+tc.spelling, []string{tc.name}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
