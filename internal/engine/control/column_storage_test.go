package control

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestIndexColumnStorageTemporaryDump(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	namespace := &catalog.Namespace{ObjectAddress: fdwUnitAddress(ids.NamespaceClassID, 99001), ObjectName: "pg_temp_1", Temporary: new(ids.Domain).Session(1)}
	source.State.SetDescriptor(namespace.Address(), namespace)
	// The temporary parent is deliberately outside the captured persistent
	// graph. Late repairs must not look it up or emit its expression index.
	relation := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 99002), ObjectNamespace: namespace.Address(), ObjectName: "temp_expression", RelationKind: catalog.IndexRelation, Columns: []catalog.Column{{Name: "expr"}}, Attributes: []int32{1}, SQLIndex: &catalog.IndexDefinition{Parent: fdwUnitAddress(ids.RelationClassID, 99003), Keys: []catalog.IndexKey{{}}}}
	source.State.SetDescriptor(relation.Address(), relation)
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog}}
	var out bytes.Buffer
	ctx := context.Background()
	writer := &dumpWriter{ctx: ctx, dst: &out}
	if err := emitDumpIndexStorage(ctx, script, writer); err != nil { t.Fatal(err) }
	if out.Len() != 0 { t.Fatalf("temporary index emitted persistent repairs: %q", out.String()) }
}

// A singleton keeps its constructor label. Exact output excludes name-vacating
// repairs, which would otherwise hide a saved-vs-heap comparison error.
func TestIndexColumnStorageConstructorDump(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keyType uint32
		saved   catalog.ColumnStorage
		want    string
	}{
		{
			name:    "substituted_override_equals_heap",
			keyType: 3642,
			saved:   catalog.ColumnStorage{Storage: "x", Compression: "p"},
			want:    "SET SESSION AUTHORIZATION DEFAULT;\nUPDATE pg_catalog.pg_attribute SET attstorage=E'x',attcompression=E'p' WHERE attrelid=E'\"public\".\"storage_sub_idx\"'::regclass AND attnum=1;\n",
		},
		{
			name:    "substituted_constructor_defaults",
			keyType: 3642,
			saved:   catalog.ColumnStorage{Storage: "p"},
		},
		{
			name:    "same_type_preserves_heap",
			keyType: uint32(sqltype.TSVector),
			saved:   catalog.ColumnStorage{Storage: "x", Compression: "p"},
		},
		{
			name:  "ordinary_constructor_copies_heap",
			saved: catalog.ColumnStorage{Storage: "x", Compression: "p"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, _, _, _ := fdwUnitSource()
			catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
			vector, ok := source.OriginalCatalog.LookupType(sqltype.TSVector)
			if !ok {
				t.Fatal("missing tsvector provider")
			}
			parent := &catalog.Relation{
				ObjectAddress: fdwUnitAddress(ids.RelationClassID, 99010),
				ObjectNamespace: fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID),
				ObjectName: "storage_sub", RelationKind: catalog.TableRelation,
				Columns: []catalog.Column{{Name: "v", Usage: vector.Usage()}},
				Attributes: []int32{7},
				ColumnMeta: catalog.ColumnMetadata{Storage: map[int32]catalog.ColumnStorage{7: {Compression: "p"}}},
			}
			relation := &catalog.Relation{
				ObjectAddress: fdwUnitAddress(ids.RelationClassID, 99011),
				ObjectNamespace: parent.Namespace(),
				ObjectName: "storage_sub_idx", RelationKind: catalog.IndexRelation,
				Columns: []catalog.Column{{Name: "v", Usage: vector.Usage()}},
				Attributes: []int32{1},
				SQLIndex: &catalog.IndexDefinition{Parent: parent.Address(), Keys: []catalog.IndexKey{
					{Attribute: 7, Class: sqltype.NewIndexClass(sqltype.IndexClassDefinition{Storage: tc.keyType})},
				}},
				ColumnMeta: catalog.ColumnMetadata{Storage: map[int32]catalog.ColumnStorage{1: tc.saved}},
			}
			source.State.SetDescriptor(parent.Address(), parent)
			source.State.SetDescriptor(relation.Address(), relation)
			script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog}}
			var out bytes.Buffer
			ctx := context.Background()
			writer := &dumpWriter{ctx: ctx, dst: &out}
			if err := emitDumpIndexStorage(ctx, script, writer); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("constructor-policy repairs:\ngot  %q\nwant %q", out.String(), tc.want)
			}
			if relation.Columns[0].Name != "v" || relation.ColumnStorage(0) != tc.saved || parent.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "x", Compression: "p"}) {
				t.Fatal("Dump changed captured index or parent")
			}
		})
	}
}
