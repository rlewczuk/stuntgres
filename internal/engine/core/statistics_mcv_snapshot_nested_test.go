package core_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// A catalog-owned domain over the retained structural carrier exercises the
// existing recursive typed traversal, not a new ordinary SQL constructor.
func statisticsTreeContainer(t *testing.T, id sqltype.ID) (*core.Engine, catalog.Read, *sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	e := engine.New()
	read := e.CatalogRead()
	tree, _ := read.LookupType(sqltype.PgNodeTree)
	domain, err := sqltype.Domain(id, "snapshot_tree_domain", "public.snapshot_tree_domain", tree.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(id+1, "_snapshot_tree_domain", domain)
	if err != nil {
		t.Fatal(err)
	}
	state := catalog.CloneState(read.State())
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}, &catalog.Type{SQLType: domain, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Array: id + 1})
	state.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id + 1)}, &catalog.Type{SQLType: array, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}})
	return e, read.WithState(state), domain, array
}

func TestStatisticsMCVSnapshotNestedTree(t *testing.T) {
	source, read, domain, array := statisticsTreeContainer(t, 45000)
	tree, _ := read.LookupType(sqltype.PgNodeTree)
	leaf, err := sqltype.StatisticsRetainedTreeValue(tree.Usage(), "(owned structural bytes)")
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := domain.Wrap(leaf)
	if err != nil {
		t.Fatal(err)
	}
	value, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -2, Length: 2}}, []sqltype.Value{wrapped, sqltype.Null(domain)})
	if err != nil {
		t.Fatal(err)
	}
	codec := core.StatisticsSnapshotCodec(t.Context(), source, read)
	encoded, err := codec.Encode(array.Usage(), value)
	if err != nil {
		t.Fatal(err)
	}
	destination, destinationRead, destinationDomain, destinationArray := statisticsTreeContainer(t, 47000)
	destinationCodec := core.StatisticsSnapshotCodec(t.Context(), destination, destinationRead)
	restored, err := destinationCodec.Decode(destinationArray.Usage(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Type() != destinationArray || restored.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -2, Length: 2}) || !restored.ArrayElement(1).IsNull() || restored.ArrayElement(0).Type() != destinationDomain {
		t.Fatal("nested remint lost shape, NULL or destination type")
	}
	base, err := restored.ArrayElement(0).BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	output, err := base.Type().Output(t.Context(), base)
	if err != nil || output != "(owned structural bytes)" {
		t.Fatal(output, err)
	}
	after, err := destinationCodec.Encode(destinationArray.Usage(), restored)
	if err != nil || !bytes.Equal(encoded, after) {
		t.Fatal("nested structural bytes changed", err)
	}
	var node typedef.DomainReplayNode
	if err := json.Unmarshal(encoded, &node); err != nil {
		t.Fatal(err)
	}
	ordinary := typedef.DomainReplayEnv{Context: t.Context(), Catalog: destinationRead}
	if _, _, err := typedef.RestoreDomainReplayNode(&ordinary, destinationArray.Usage(), &node); err == nil {
		t.Fatal("nested tree escaped ordinary replay rejection")
	}
	// Native reconstruction uses the destination domain as the embedded element
	// identity, despite preserving source semantic constructor bytes exactly.
	int4, _ := destinationRead.LookupType(sqltype.Int4)
	integer, err := int4.Input(t.Context(), "1", -1)
	if err != nil {
		t.Fatal(err)
	}
	data := &storage.ExtendedMCV{Rows: 1, Items: []storage.ExtendedMCVItem{{Count: 1, MarginalCounts: [8]uint64{1, 1}}}}
	data.Dictionaries[0] = []sqltype.Value{restored}
	data.Dictionaries[1] = []sqltype.Value{integer}
	mcvType, _ := destinationRead.LookupType(sqltype.PgMCVList)
	mcv, err := storage.BuildExtendedMCV(t.Context(), mcvType, []sqltype.Usage{destinationArray.Usage(), int4.Usage()}, data)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := mcvType.AppendBinaryWithEnvironment(t.Context(), nil, mcv, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	// 14-byte header, two type OIDs, two 20-byte dimension records;
	// dictionary's four-byte length then ArrayType's ndim,dataoffset,element OID.
	if len(raw) < 78 || binary.LittleEndian.Uint32(raw[14:]) != uint32(destinationArray.ID()) || binary.LittleEndian.Uint32(raw[74:]) != uint32(destinationDomain.ID()) {
		t.Fatal("source array/type OID survived native reconstruction")
	}
}
