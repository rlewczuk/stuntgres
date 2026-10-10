package engine

import (
	"fmt"
	"testing"
)

func TestCommentInquiryBuiltinDescriptions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, tc := range []struct {
		oid  int
		want any
	}{
		{16, "boolean, format 't'/'f'"}, {17, "variable-length string, binary values escaped"}, {18, "single character"},
		{19, "63-byte type for storing system identifiers"}, {20, "~18 digit integer, 8-byte storage"}, {21, "-32 thousand to 32 thousand, 2-byte storage"},
		{23, "-2 billion to 2 billion integer, 4-byte storage"}, {25, "variable-length string, no limit specified"}, {26, "object identifier(oid), maximum 4 billion"},
		{700, "single-precision floating point number, 4-byte storage"}, {701, "double-precision floating point number, 8-byte storage"},
		{705, "pseudo-type representing an undetermined type"}, {1042, "'char(length)' blank-padded string, fixed storage length"},
		{1043, "'varchar(length)' non-blank-padded string, variable storage length"}, {1082, "date"}, {1083, "time of day"},
		{1114, "date and time"}, {1184, "date and time with time zone"}, {1186, "time interval, format 'number units ...'"},
		{1266, "time of day with time zone"}, {1700, "'numeric(precision, scale)' arbitrary precision number"}, {2205, "registered class"},
		{2278, "pseudo-type for the result of a function with no real result"}, {2950, "UUID"}, {3802, "Binary JSON"}, {1003, nil},
	} {
		got := tuples(roleExecute(t, e, op, fmt.Sprintf("SELECT obj_description(%d,'pg_type')", tc.oid)))
		if len(got) != 1 || got[0][0] != tc.want {
			t.Fatal(tc.oid, got, tc.want)
		}
		got = tuples(roleExecute(t, e, op, fmt.Sprintf("SELECT obj_description(%d,'pg_namespace')", tc.oid)))
		if len(got) != 1 || got[0][0] != nil {
			t.Fatal("wrong class", tc.oid, got)
		}
	}
}
