package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareNullProviderFamilies(t *testing.T) {
	p := fixture.NewScenario(t)
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
		fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE p(id INT,a "+tc.typ+",b "+tc.typ+")", 0))
		fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO p VALUES(1,"+tc.equal+","+tc.equal+"),(2,"+tc.equal+","+tc.unequal+"),(3,NULL,NULL),(4,NULL,"+tc.equal+")", 4))
		if tc.typ == "NUMERIC" {
			fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO p VALUES(5,'1.0','1.000')", 1))
		}
		if tc.nan {
			fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO p VALUES(6,'NaN','NaN')", 1))
		}
		want := [][]any{{int64(1), false, true, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), true, false, true, false, false, true}, {int64(4), true, false, false, true, true, false}}
		if tc.typ == "NUMERIC" {
			want = append(want, []any{int64(5), false, true, false, true, false, true})
		}
		if tc.nan {
			want = append(want, []any{int64(6), false, true, false, true, false, true})
		}
		step := fixture.Select("SELECT id,a IS NULL,a IS NOT NULL,b ISNULL,b NOTNULL,a IS DISTINCT FROM b,b IS NOT DISTINCT FROM a FROM p ORDER BY id", []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want)
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
		fixture.ScalarStep(t, p, fixture.Exec("DROP TABLE p", 0))
	}
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE incompatible(a INTEGER,b BOOLEAN)", 0))
	for _, insert := range []bool{false, true} {
		if insert {
			fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO incompatible VALUES(NULL,NULL)", 1))
		}
		for _, tc := range []struct {
			q, message string
			position   string
		}{
			{"SELECT a IS DISTINCT FROM b FROM incompatible", "operator does not exist: integer = boolean", "10"},
			{"SELECT b IS NOT DISTINCT FROM a FROM incompatible", "operator does not exist: boolean = integer", "10"},
		} {
			step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
			step.SelectRows = true
			step.ErrorStage = "execute"
			fixture.ScalarStep(t, p, step)
			step.ErrorStage = "prepare"
			fixture.ScalarStep(t, p, fixture.Prepared(step))
		}
	}
}
