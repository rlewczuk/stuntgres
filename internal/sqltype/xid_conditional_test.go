package sqltype

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIdentifierConditionalEqualitySignatures(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		left, right ID
		want        EqualitySignature
	}{
		{Xid, Xid, EqualitySignature{Xid, Xid}}, {Cid, Cid, EqualitySignature{Cid, Cid}}, {Xid8, Xid8, EqualitySignature{Xid8, Xid8}},
		{Xid, Int4, EqualitySignature{Xid, Int4}}, {Xid, Int2, EqualitySignature{Xid, Int4}},
	} {
		got, err := ResolveEquality(types[tc.left], types[tc.right])
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, id := range []ID{Xid, Cid, Xid8} {
		for _, leftUnknown := range []bool{false, true} {
			left, right := types[id], types[id]
			if leftUnknown {
				left = nil
			} else {
				right = nil
			}
			got, err := ResolveEquality(left, right)
			if err != nil || got != (EqualitySignature{id, id}) {
				t.Fatal(id, leftUnknown, got, err)
			}
		}
	}
	for _, pair := range [][2]ID{{Int4, Xid}, {Int2, Xid}, {Oid, Xid}, {Xid, Oid}, {Xid, Cid}, {Cid, Xid}, {Xid, Xid8}, {Xid8, Xid}, {Cid, Xid8}, {Xid8, Cid}, {Xid, Int8}} {
		_, err := ResolveEquality(types[pair[0]], types[pair[1]])
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
			t.Fatal(pair, err)
		}
	}
}
