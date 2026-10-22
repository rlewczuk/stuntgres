package index

import (
	"stuntgres/internal/engine/catalog"
	"testing"
)

func TestAccessMethodProperty(t *testing.T) {
	properties := []string{"can_order", "can_unique", "can_multi_col", "can_exclude", "can_include"}
	for _, tc := range []struct {
		method catalog.IndexMethod
		want   [5]bool
	}{
		{catalog.BtreeMethod, [5]bool{true, true, true, true, true}},
		{catalog.HashMethod, [5]bool{false, false, false, true, false}},
		{catalog.GistMethod, [5]bool{false, false, true, true, true}},
	} {
		for i, name := range properties {
			value, known := MethodProperty(tc.method, name)
			if !known || value != tc.want[i] {
				t.Fatal(tc, name, value, known)
			}
		}
		if _, known := MethodProperty(tc.method, "asc"); known {
			t.Fatal("wrong property level")
		}
	}
	if _, known := MethodProperty(catalog.IndexMethod(255), "can_order"); known {
		t.Fatal("unknown implementation admitted")
	}
}
