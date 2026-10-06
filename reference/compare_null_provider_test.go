//go:build reference

package reference_test

import "testing"

func TestReferenceCompareNullProviderFamilies(t *testing.T) {
	p := newReferencePair(t)
	for _, tc := range []struct {
		typ, equal, unequal string
		nan                 bool
	}{
		{"SMALLINT", "1", "2", false}, {"INTEGER", "1", "2", false}, {"BIGINT", "2147483648", "2147483649", false},
		{"TEXT", "'x'", "'y'", false}, {"BOOLEAN", "TRUE", "FALSE", false},
		{"NUMERIC", "'1.00'", "'2.0'", true}, {"REAL", "'1.25'", "'2.5'", true}, {"DOUBLE PRECISION", "'1.25'", "'2.5'", true},
		{"UUID", "'123e4567-e89b-12d3-a456-426614174000'", "'123e4567-e89b-12d3-a456-426614174001'", false},
		{"TIME", "'12:34:56'", "'12:34:57'", false}, {"TIMESTAMP", "'2020-01-02 12:34:56'", "'2020-01-02 12:34:57'", false},
	} {
		p.step(t, referenceExec("CREATE TABLE p(id INT,a "+tc.typ+",b "+tc.typ+")", 0))
		p.step(t, referenceExec("INSERT INTO p VALUES(1,"+tc.equal+","+tc.equal+"),(2,"+tc.equal+","+tc.unequal+"),(3,NULL,NULL),(4,NULL,"+tc.equal+")", 4))
		if tc.typ == "NUMERIC" {
			p.step(t, referenceExec("INSERT INTO p VALUES(5,'1.0','1.000')", 1))
		}
		if tc.nan {
			p.step(t, referenceExec("INSERT INTO p VALUES(6,'NaN','NaN')", 1))
		}
		want := [][]any{{int64(1), false, true, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), true, false, true, false, false, true}, {int64(4), true, false, false, true, true, false}}
		if tc.typ == "NUMERIC" {
			want = append(want, []any{int64(5), false, true, false, true, false, true})
		}
		if tc.nan {
			want = append(want, []any{int64(6), false, true, false, true, false, true})
		}
		step := referenceSelect("SELECT id,a IS NULL,a IS NOT NULL,b ISNULL,b NOTNULL,a IS DISTINCT FROM b,b IS NOT DISTINCT FROM a FROM p ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want)
		p.step(t, step)
		p.step(t, referencePrepared(step))
		p.step(t, referenceExec("DROP TABLE p", 0))
	}
	p.step(t, referenceExec("CREATE TABLE incompatible(a INTEGER,b BOOLEAN)", 0))
	for _, insert := range []bool{false, true} {
		if insert {
			p.step(t, referenceExec("INSERT INTO incompatible VALUES(NULL,NULL)", 1))
		}
		for _, tc := range []struct {
			q, message string
			position   string
		}{
			{"SELECT a IS DISTINCT FROM b FROM incompatible", "operator does not exist: integer = boolean", "10"},
			{"SELECT b IS NOT DISTINCT FROM a FROM incompatible", "operator does not exist: boolean = integer", "10"},
		} {
			step := referenceImplicitError(tc.q, referenceDiagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		}
	}
}
