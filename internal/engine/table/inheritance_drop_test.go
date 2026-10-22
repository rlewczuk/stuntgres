package table

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestInheritanceDropClosure(t *testing.T) {
	f := newPendingFixture()
	column := f.p.Address()
	column.SubID = 1
	missing := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 999}
	for _, tc := range []struct {
		name  string
		roots []ids.ObjectAddress
		want  []ids.ObjectAddress
	}{
		{"descendants", []ids.ObjectAddress{f.c1.Address()}, []ids.ObjectAddress{f.c1.Address(), f.c2.Address()}},
		{"sorted and shared", []ids.ObjectAddress{f.q.Address(), f.c2.Address(), f.p.Address()}, []ids.ObjectAddress{f.p.Address(), f.c1.Address(), f.c2.Address(), f.q.Address()}},
		{"column and missing roots", []ids.ObjectAddress{column, missing, f.namespaceAddr}, nil},
	} {
		if got := InheritanceDropClosure(f.view, tc.roots); !slices.Equal(got, tc.want) {
			t.Errorf("%s: closure %v, want %v", tc.name, got, tc.want)
		}
	}
}
