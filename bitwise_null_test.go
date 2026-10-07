package stuntgres_test

import "testing"

func TestBitwiseTypedNullBothSides(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1)", 1)
	for _, tc := range []struct{ q, typ string }{
		{"SELECT NULL::smallint & 1::smallint,1::smallint & NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint | 1::smallint,1::smallint | NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint # 1::smallint,1::smallint # NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint << 1::integer,1::smallint << NULL::integer FROM one", "INT2"},
		{"SELECT NULL::smallint >> 1::integer,1::smallint >> NULL::integer FROM one", "INT2"},
		{"SELECT NULL::integer & 1::integer,1::integer & NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer | 1::integer,1::integer | NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer # 1::integer,1::integer # NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer << 1::integer,1::integer << NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer >> 1::integer,1::integer >> NULL::integer FROM one", "INT4"},
		{"SELECT NULL::bigint & 1::bigint,1::bigint & NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint | 1::bigint,1::bigint | NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint # 1::bigint,1::bigint # NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint << 1::integer,1::bigint << NULL::integer FROM one", "INT8"},
		{"SELECT NULL::bigint >> 1::integer,1::bigint >> NULL::integer FROM one", "INT8"},
	} {
		arithmeticRows(t, db, tc.q, []string{tc.typ, tc.typ}, [][]any{{nil, nil}})
	}
}
