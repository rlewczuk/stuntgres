package typedef

import (
	"math"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPGEnumSortInsertion(t *testing.T) {
	if got := enumInsertionOrder(nil, 0); got != 1 {
		t.Fatal("empty", got)
	}
	members := []sqltype.EnumMember{{ID: 91, Label: "a", SortOrder: 1}, {ID: 21, Label: "b", SortOrder: 2}}
	for _, tc := range []struct {
		at   int
		want float32
	}{{0, 0}, {1, 1.5}, {2, 3}} {
		if got := enumInsertionOrder(members, tc.at); got != tc.want {
			t.Fatal("insertion", tc, got)
		}
	}
	dense := []sqltype.EnumMember{{ID: 91, Label: "a", SortOrder: 1}, {ID: 21, Label: "b", SortOrder: math.Nextafter32(1, 2)}}
	codec, err := sqltype.EnumWithMembers(45000, "dense", "public.dense", dense)
	if err != nil {
		t.Fatal(err)
	}
	if got := enumInsertionOrder(dense, 1); got != 1.5 || dense[0].SortOrder != 1 || dense[1].SortOrder != 2 || dense[0].ID != 91 || dense[1].ID != 21 {
		t.Fatal("collapse must renumber and retry without reminting", got, dense)
	}
	old, _ := codec.EnumMemberAt(1)
	if old.SortOrder != math.Nextafter32(1, 2) {
		t.Fatal("renumber mutated previous generation", old)
	}
	negative := []sqltype.EnumMember{{ID: 1, Label: "a", SortOrder: 0}}
	if got := enumInsertionOrder(negative, 0); got != -1 {
		t.Fatal("negative prepend", got)
	}
}
