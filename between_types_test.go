package stuntgres_test

import (
	"testing"

	"stuntgres"
)

func TestBetweenProvidersAndIndependentTyping(t *testing.T) {
	_, db := betweenSetup(t)
	for _, expr := range []string{
		"2147483647 BETWEEN 2147483647 AND 2147483648", "9007199254740993 BETWEEN 9007199254740992 AND 9007199254740993",
		"-9223372036854775808 BETWEEN -9223372036854775808 AND 9223372036854775807", "FALSE BETWEEN FALSE AND TRUE", "TRUE BETWEEN FALSE AND TRUE", "'z' BETWEEN 'a' AND 'é'", "'2' BETWEEN '10' AND '3'",
	} {
		compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM t WHERE id=1", []string{"BOOL"}, [][]any{{true}})
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
			db := openLocal(t, stuntgres.NewDriver(), "between-provider")
			execSQL(t, db, "CREATE TABLE p(a "+tc.typ+",l "+tc.typ+",u "+tc.typ+")")
			execSQL(t, db, "INSERT INTO p VALUES($1,$2,$3)", tc.value, tc.lower, tc.upper)
			compareOpsDirectPrepared(t, db, "SELECT a BETWEEN l AND u,a NOT BETWEEN l AND u,a BETWEEN SYMMETRIC u AND l,a NOT BETWEEN SYMMETRIC u AND l FROM p", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, true, false}})
		})
	}
	execSQL(t, db, "CREATE TABLE m(i INTEGER,s TEXT,j BIGINT)")
	execSQL(t, db, "INSERT INTO m VALUES(1,'3',3)")
	compareOpsDirectPrepared(t, db, "SELECT '2' BETWEEN i AND s,'2' BETWEEN SYMMETRIC i AND s FROM m", []string{"BOOL", "BOOL"}, [][]any{{true, true}})
	for _, form := range betweenForms {
		stmt, err := db.Prepare("SELECT $1 " + form + " i AND j FROM m")
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{2, nil, 2} {
			var want any = form == "BETWEEN" || form == "BETWEEN SYMMETRIC"
			if arg == nil {
				want = nil
			}
			rows, err := stmt.Query(arg)
			compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{want}})
		}
		stmt.Close()
		for _, expr := range []string{"j " + form + " $1 AND j", "i " + form + " i AND $1"} {
			compareOpsDirectPrepared(t, db, "SELECT "+expr+" FROM m", []string{"BOOL"}, [][]any{{form == "BETWEEN" || form == "BETWEEN SYMMETRIC"}}, 3)
		}
	}
	compareOpsDirectPrepared(t, db, "SELECT $1 BETWEEN $2 AND $3 FROM m", []string{"BOOL"}, [][]any{{true}}, "2", "10", "3")
}
