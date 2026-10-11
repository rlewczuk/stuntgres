package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenProviders(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, expr := range []string{"2147483647 BETWEEN 2147483647 AND 2147483648", "9007199254740993 BETWEEN 9007199254740992 AND 9007199254740993", "-9223372036854775808 BETWEEN -9223372036854775808 AND 9223372036854775807", "FALSE BETWEEN FALSE AND TRUE", "TRUE BETWEEN FALSE AND TRUE", "'z' BETWEEN 'a' AND 'é'", "'2' BETWEEN '10' AND '3'"} {
		betweenReferenceBoth(t, p, fixture.Select("SELECT "+expr+" FROM t WHERE id=1", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
	}
	for _, tc := range []struct{ typ, value, lower, upper string }{
		{"SMALLINT", "32767", "-32768", "32767"}, {"BIGINT", "9007199254740993", "9007199254740992", "9007199254740993"},
		{"NAME", "b", "a", "c"}, {"CHAR(3)", "b ", "b", "b  "}, {"VARCHAR(3)", "b", "a", "c"},
		{"DATE", "2000-01-02", "2000-01-01", "2000-01-03"}, {"TIME(3)", "12:00:00.123", "11:00:00.123", "13:00:00.123"},
		{"TIMESTAMP(3)", "2000-01-02 12:00:00.123", "2000-01-01 12:00:00.123", "2000-01-03 12:00:00.123"},
		{"UUID", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000003"},
		{"BYTEA", `\x02`, `\x01`, `\x03`}, {"NUMERIC", "9007199254740993.00", "9007199254740992", "9007199254740993.0"},
		{"NUMERIC", "NaN", "Infinity", "NaN"}, {"REAL", "NaN", "Infinity", "NaN"}, {"DOUBLE PRECISION", "-0", "0", "0"},
	} {
		t.Run(tc.typ+tc.value, func(t *testing.T) {
			p := fixture.NewScenario(t)
			fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE p(a "+tc.typ+",l "+tc.typ+",u "+tc.typ+")", 0))
			fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO p VALUES($1,$2,$3)", 1, tc.value, tc.lower, tc.upper))
			betweenReferenceBoth(t, p, fixture.Select("SELECT a BETWEEN l AND u,a NOT BETWEEN l AND u,a BETWEEN SYMMETRIC u AND l,a NOT BETWEEN SYMMETRIC u AND l FROM p", []string{"?column?", "?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, false}}))
		})
	}
}

func TestReferenceBetweenIndependentTyping(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.ScalarStep(t, p, fixture.Exec("CREATE TABLE m(i INTEGER,s TEXT,j BIGINT)", 0))
	fixture.ScalarStep(t, p, fixture.Exec("INSERT INTO m VALUES(1,'3',3)", 1))
	betweenReferenceBoth(t, p, fixture.Select("SELECT '2' BETWEEN i AND s,'2' BETWEEN SYMMETRIC i AND s FROM m", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	for _, form := range betweenReferenceForms {
		for _, arg := range []any{2, nil, 2} {
			var want any = form == "BETWEEN" || form == "BETWEEN SYMMETRIC"
			if arg == nil {
				want = nil
			}
			betweenReferenceBoth(t, p, fixture.Select("SELECT $1 "+form+" i AND j FROM m", []string{"?column?"}, []string{"BOOL"}, [][]any{{want}}, arg))
		}
	}
	betweenReferenceBoth(t, p, fixture.Select("SELECT $1 BETWEEN $2 AND $3 FROM m", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}, "2", "10", "3"))
}
