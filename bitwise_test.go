package stuntgres_test

import "testing"

func TestBitwiseManualValuesAndWidths(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1)", 1)
	for _, tc := range []struct {
		query string
		typ   string
	}{
		{"SELECT 91 & 15,32 | 3,17 # 5,~1,1 << 4,8 >> 2 FROM one", "INT4"},
		{"SELECT 91::smallint & 15::smallint,32::smallint | 3::smallint,17::smallint # 5::smallint,~1::smallint,1::smallint << 4,8::smallint >> 2 FROM one", "INT2"},
		{"SELECT 91::bigint & 15::bigint,32::bigint | 3::bigint,17::bigint # 5::bigint,~1::bigint,1::bigint << 4,8::bigint >> 2 FROM one", "INT8"},
	} {
		arithmeticRows(t, db, tc.query,
			[]string{tc.typ, tc.typ, tc.typ, tc.typ, tc.typ, tc.typ},
			[][]any{{int64(11), int64(35), int64(20), int64(-2), int64(16), int64(2)}})
	}
}
