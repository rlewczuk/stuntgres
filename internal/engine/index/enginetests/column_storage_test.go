package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestIndexColumnStorageConstructor(t *testing.T) {
	view := engine.New().CatalogRead()
	vector, ok := view.LookupType(sqltype.TSVector)
	if !ok {
		t.Fatal("missing tsvector provider")
	}
	parent := &catalog.Relation{
		Columns:    []catalog.Column{{Name: "v", Usage: vector.Usage()}},
		Attributes: []int32{7},
		ColumnMeta: catalog.ColumnMetadata{Storage: map[int32]catalog.ColumnStorage{
			7: {Storage: "e", Compression: "p"},
		}},
	}
	// REL_18_6 catalog/index.c copies simple attributes, then resets BOTH
	// policies only if the effective key type differs from that input type.
	// 3642 is the supported tsvector GiST class's gtsvector storage type.
	substituted := sqltype.NewIndexClass(sqltype.IndexClassDefinition{Storage: 3642})
	sameType := sqltype.NewIndexClass(sqltype.IndexClassDefinition{Storage: uint32(sqltype.TSVector)})
	for _, tc := range []struct {
		name       string
		definition catalog.IndexDefinition
		want       []catalog.ColumnStorage
	}{
		{
			name:       "every_simple_occurrence",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 7}, {Attribute: 7}}},
			want:       []catalog.ColumnStorage{{Storage: "e", Compression: "p"}, {Storage: "e", Compression: "p"}},
		},
		{
			name:       "substituted_key_resets_compression",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 7, Class: substituted}}},
			want:       []catalog.ColumnStorage{{Storage: "p"}},
		},
		{
			name:       "same_effective_key_type_preserves_policies",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 7, Class: sameType}}},
			want:       []catalog.ColumnStorage{{Storage: "e", Compression: "p"}},
		},
		{
			name:       "expression_uses_type_defaults",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{}}},
			want:       []catalog.ColumnStorage{{Storage: "x"}},
		},
		{
			name:       "substituted_expression_uses_key_type_defaults",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{Class: substituted}}},
			want:       []catalog.ColumnStorage{{Storage: "p"}},
		},
		{
			name:       "include_copies_parent",
			definition: catalog.IndexDefinition{Keys: []catalog.IndexKey{{Attribute: 7, Class: substituted}}, Include: []int32{7}},
			want:       []catalog.ColumnStorage{{Storage: "p"}, {Storage: "e", Compression: "p"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			relation := &catalog.Relation{SQLIndex: &tc.definition}
			for i := range tc.want {
				relation.Columns = append(relation.Columns, parent.Columns[0])
				relation.Attributes = append(relation.Attributes, int32(i+1))
			}
			for slot, want := range tc.want {
				var got catalog.ColumnStorage
				var err error
				allocations, bytes := util.AllocationsPerCall(100, func() {
					got, err = index.ConstructorColumnStorage(view, parent, relation, slot)
				})
				if err != nil || got != want {
					t.Fatalf("prediction slot %d: got %+v, %v; want %+v", slot, got, err, want)
				}
				if allocations != 0 || bytes != 0 {
					t.Fatalf("prediction allocated: %d allocations, %d bytes", allocations, bytes)
				}
				if relation.ColumnMeta.Storage != nil {
					t.Fatal("prediction mutated constructor metadata")
				}
			}
			if err := index.CopyColumnStorage(view, parent, relation); err != nil {
				t.Fatal(err)
			}
			for slot, want := range tc.want {
				if got := relation.ColumnStorage(slot); got != want {
					t.Errorf("slot %d: got %+v want %+v", slot, got, want)
				}
			}
			if got := parent.ColumnStorage(0); got != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) {
				t.Fatal("constructor changed parent", got)
			}
			before := relation.ColumnStorage(0)
			override := catalog.ColumnStorage{Storage: "x", Compression: "l"}
			changed := relation.WithColumnStorage(0, override)
			if changed == relation || changed.ColumnStorage(0) != override || relation.ColumnStorage(0) != before {
				t.Fatal("effective constructor properties lost immutable override")
			}
		})
	}
}
