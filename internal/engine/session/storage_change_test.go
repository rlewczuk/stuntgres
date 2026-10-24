package session

import (
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func fillKeys(fills map[int32]sqltype.Value) []int32 {
	return slices.Sorted(maps.Keys(fills))
}

func TestStorageChangeMergeFillsReplacesSharedMap(t *testing.T) {
	shared := map[int32]sqltype.Value{1: {}, 2: {}}
	change := StorageChange{Fills: shared}
	change.MergeFills(map[int32]sqltype.Value{3: {}}, &catalog.Relation{Attributes: []int32{1, 3}})
	if got := fillKeys(change.Fills); !slices.Equal(got, []int32{1, 3}) {
		t.Fatalf("merged fills = %v, want [1 3]", got)
	}
	if got := fillKeys(shared); !slices.Equal(got, []int32{1, 2}) {
		t.Fatalf("shared fills mutated to %v", got)
	}
}

func TestStorageChangeMergeFillsWithoutFillsKeepsNil(t *testing.T) {
	var change StorageChange
	change.MergeFills(nil, &catalog.Relation{Attributes: []int32{1}})
	if change.Fills != nil {
		t.Fatalf("fills = %v, want nil", change.Fills)
	}
	change.MergeFills(map[int32]sqltype.Value{2: {}}, &catalog.Relation{Attributes: []int32{1}})
	if change.Fills == nil || len(change.Fills) != 0 {
		t.Fatalf("fills of dropped attribute = %v, want an empty map", change.Fills)
	}
}
