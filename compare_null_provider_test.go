package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCompareNullProviderFamilies(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
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
		t.Run(tc.typ, func(t *testing.T) {
			execSQL(t, db, "CREATE TABLE p(id INT,a "+tc.typ+",b "+tc.typ+")")
			execSQL(t, db, "INSERT INTO p VALUES(1,"+tc.equal+","+tc.equal+"),(2,"+tc.equal+","+tc.unequal+"),(3,NULL,NULL),(4,NULL,"+tc.equal+")")
			if tc.typ == "NUMERIC" {
				execSQL(t, db, "INSERT INTO p VALUES(5,'1.0','1.000')")
			}
			if tc.nan {
				execSQL(t, db, "INSERT INTO p VALUES(6,'NaN','NaN')")
			}
			want := [][]any{{int64(1), false, true, false, true, false, true}, {int64(2), false, true, false, true, true, false}, {int64(3), true, false, true, false, false, true}, {int64(4), true, false, false, true, true, false}}
			if tc.typ == "NUMERIC" {
				want = append(want, []any{int64(5), false, true, false, true, false, true})
			}
			if tc.nan {
				want = append(want, []any{int64(6), false, true, false, true, false, true})
			}
			q := "SELECT id,a IS NULL,a IS NOT NULL,b ISNULL,b NOTNULL,a IS DISTINCT FROM b,b IS NOT DISTINCT FROM a FROM p ORDER BY id"
			rows, err := db.Query(q)
			compareNullRows(t, rows, err, []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want)
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = stmt.Query()
			compareNullRows(t, rows, err, []string{"id", "?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want)
			stmt.Close()
			execSQL(t, db, "DROP TABLE p")
		})
	}
	execSQL(t, db, "CREATE TABLE incompatible(a INTEGER,b BOOLEAN)")
	for _, q := range []string{"SELECT a IS DISTINCT FROM b FROM incompatible", "SELECT b IS NOT DISTINCT FROM a FROM incompatible"} {
		rows, err := db.Query(q)
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatal("empty incompatible table accepted", q)
		}
	}
	execSQL(t, db, "INSERT INTO incompatible VALUES(NULL,NULL)")
	rows, err := db.Query("SELECT a IS DISTINCT FROM b FROM incompatible")
	if rows != nil {
		rows.Close()
	}
	if err == nil {
		t.Fatal("typed NULLs bypassed equality typing")
	}
}
