package control

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestInheritanceColumnStorageSchedule(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	catalog.InstallBuiltinTypes(source.State, sqltype.Builtins(), source.Bootstrap)
	textType, ok := source.OriginalCatalog.LookupType(sqltype.Text)
	if !ok { t.Fatal("missing text type") }
	parent := attributeUnitRelation(99100, "parent", 1)
	child := attributeUnitRelation(99101, "child", 1)
	unrelated := attributeUnitRelation(99102, "unrelated", 1)
	attributeUnitParent(child, parent)
	attributeUnitLive(parent, 1, "t", true, nil, 0)
	attributeUnitLive(child, 1, "t", false, parent, 1)
	attributeUnitLive(unrelated, 1, "t", true, nil, 0)
	for _, r := range []*catalog.Relation{parent, child, unrelated} {
		r.ObjectNamespace = fdwUnitAddress(ids.NamespaceClassID, ids.PublicNamespaceOID)
		r.Columns[0].Usage = textType.Usage()
		source.State.SetDescriptor(r.Address(), r)
	}
	parent.ColumnMeta.Storage = map[int32]catalog.ColumnStorage{1: {Storage: "e", Compression: "p"}}
	script := &dumpScript{Capture: &Capture{CatalogView: source.CatalogView, OriginalCatalog: source.OriginalCatalog,
		Tables: []table.DumpRelation{{Descriptor: parent}, {Descriptor: child}, {Descriptor: unrelated}}}}
	if err := script.prepareAttributeHistory(); err != nil { t.Fatal(err) }
	ctx := context.Background()
	if err := script.prepareAttributeSchedules(ctx); err != nil { t.Fatal(err) }
	if len(script.attributeSchedules) != 0 { t.Fatal("source-shaped component forced shape scheduling") }
	if !script.deferredColumnStorage[parent.Address()] || !script.deferredColumnStorage[child.Address()] || script.deferredColumnStorage[unrelated.Address()] {
		t.Fatal("ordinary component membership lost or widened")
	}
	var out bytes.Buffer
	writer := &dumpWriter{ctx: ctx, dst: &out}
	if err := emitRelationColumnStorage(script, writer, parent); err != nil { t.Fatal(err) }
	if out.Len() != 0 { t.Fatal("final parent policy leaked into constructor interval") }
	if err := emitDumpDeferredColumnStorage(ctx, script, writer); err != nil { t.Fatal(err) }
	want := "SET SESSION AUTHORIZATION DEFAULT;\n" +
		"ALTER TABLE ONLY \"public\".\"child\" ALTER COLUMN \"t\" SET STORAGE EXTENDED;\n" +
		"ALTER TABLE ONLY \"public\".\"child\" ALTER COLUMN \"t\" SET COMPRESSION default;\n" +
		"ALTER TABLE ONLY \"public\".\"parent\" ALTER COLUMN \"t\" SET STORAGE EXTERNAL;\n" +
		"ALTER TABLE ONLY \"public\".\"parent\" ALTER COLUMN \"t\" SET COMPRESSION pglz;\n"
	if out.String() != want { t.Fatalf("final component pass got %q want %q", out.String(), want) }
	// A same-name saved default index must be repaired even when it equals
	// the final heap prediction: construction and final ONLY updates intervened.
	idx := &catalog.Relation{ObjectAddress: fdwUnitAddress(ids.RelationClassID, 99103), ObjectNamespace: child.Namespace(),
		ObjectName: "child_idx", RelationKind: catalog.IndexRelation,
		Columns: []catalog.Column{{Name: "t", Usage: textType.Usage()}}, Attributes: []int32{1},
		SQLIndex: &catalog.IndexDefinition{Parent: child.Address(), Keys: []catalog.IndexKey{{Attribute: 1}}}}
	source.State.SetDescriptor(idx.Address(), idx)
	out.Reset()
	if err := emitDumpIndexStorage(ctx, script, writer); err != nil { t.Fatal(err) }
	if !strings.Contains(out.String(), `UPDATE pg_catalog.pg_attribute SET attstorage=E'x',attcompression=E'' WHERE attrelid=E'"public"."child_idx"'::regclass AND attnum=1;`) {
		t.Fatal("deferred component skipped exact default index repair", out.String())
	}
	if parent.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "e", Compression: "p"}) || child.ColumnStorage(0) != (catalog.ColumnStorage{Storage: "x"}) {
		t.Fatal("scheduler mutated captured policies")
	}
}
