package sqltype

import (
	"context"
	"testing"
)

func TestAlterTypeBoundedCollationUsage(t *testing.T) {
	for _, id := range []ID{Text, VarChar, BPChar, Name} {
		d := testTypes()[id]
		for _, collation := range []uint32{100, 950, 951} {
			u := d.Usage()
			u.Collation.ID = collation
			if !u.Valid() {
				t.Fatal(u)
			}
			provider, err := BindOrdering(u)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{"A", "Z"}, {"Z", "a"}, {"a", "é"}} {
				n, err := provider.Compare(context.Background(), String(d, pair[0]), String(d, pair[1]), InputEnvironment{})
				if err != nil || n >= 0 {
					t.Fatal(id, collation, pair, n, err)
				}
			}
		}
		u := d.Usage()
		u.Collation.ID = 999999
		if u.Valid() {
			t.Fatal("unbounded identity admitted", u)
		}
	}
	u := testTypes()[Int4].Usage()
	u.Collation = CollationRef{ID: 950, Derivation: Default}
	if u.Valid() {
		t.Fatal("noncollatable target admitted")
	}
	if _, err := BindOrdering(u); err == nil {
		t.Fatal("noncollatable ordering admitted")
	}
}
