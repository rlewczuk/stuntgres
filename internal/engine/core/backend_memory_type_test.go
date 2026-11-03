package core_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func backendMemoryNominalPair(t *testing.T, view catalog.Read) (*sqltype.Descriptor, *sqltype.Descriptor) {
	t.Helper()
	relation := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.BackendMemoryContextsOID})
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12144})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12143})
	if relation.RowType != 12144 || row.Array != 12143 || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() || row.Owner != relation.Owner || array.Owner != relation.Owner {
		t.Fatal("nominal catalog reciprocity", relation, row, array)
	}
	if !row.SQLType.Concrete() || !row.SQLType.IsComposite() || !row.SQLType.IsBuiltin() || row.SQLType.IsUserDefined() || row.SQLType.CompositeLen() != 10 || !array.SQLType.Concrete() || !array.SQLType.IsBuiltin() || array.SQLType.Element() != row.SQLType {
		t.Fatal("nominal builtin value authority", row.SQLType, array.SQLType)
	}
	for i, column := range relation.Columns {
		member := row.SQLType.CompositeMember(i)
		owned, ok := view.LookupType(column.Usage.Type.ID())
		if !ok || member.Attribute != relation.Attributes[i] || member.Name != column.Name || member.Usage != column.Usage || member.Usage.Type != owned {
			t.Fatal("noncurrent nominal member", i, member, column)
		}
	}
	if other := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 12357}); other.SQLType.Concrete() {
		t.Fatal("unrelated metadata-only AIO row was promoted", other)
	}
	return row.SQLType, array.SQLType
}

func TestBackendMemoryNominalRowType(t *testing.T) {
	e := engine.New()
	oldView := e.CatalogRead()
	oldRow, oldArray := backendMemoryNominalPair(t, oldView)
	fields := make([]sqltype.Value, oldRow.CompositeLen())
	for i := range fields { fields[i] = sqltype.Null(oldRow.CompositeMember(i).Usage.Type) }
	retainedRow, err := sqltype.OwnedCompositeValue(oldRow, fields)
	if err != nil { t.Fatal(err) }
	retainedArray, err := sqltype.OwnedArrayValue(oldArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{retainedRow})
	if err != nil { t.Fatal(err) }
	before, err := oldArray.Output(context.Background(), retainedArray)
	if err != nil { t.Fatal(err) }
	if err := engine.Reset(context.Background(), e); err != nil { t.Fatal(err) }
	currentRow, currentArray := backendMemoryNominalPair(t, e.CatalogRead())
	if currentRow == oldRow || currentArray == oldArray || currentRow.CompositeMember(3).Usage.Type == oldRow.CompositeMember(3).Usage.Type {
		t.Fatal("replacement retained source descriptor generation")
	}
	if retainedRow.Type() != oldRow || retainedArray.Type() != oldArray || retainedArray.ArrayElement(0).Type() != oldRow || oldArray.Element() != oldRow || oldRow.CompositeMember(3).Usage.Type != oldView.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType {
		t.Fatal("replacement mutated retained nominal backing")
	}
	if after, err := oldArray.Output(context.Background(), retainedArray); err != nil || after != before { t.Fatal("retained codec changed", after, before, err) }
	destination := engine.New()
	destinationRow, destinationArray := backendMemoryNominalPair(t, destination.CatalogRead())
	if destinationRow == currentRow || destinationArray == currentArray || destinationArray.Element() != destinationRow || destinationRow.CompositeMember(4).Usage.Type == currentRow.CompositeMember(4).Usage.Type {
		t.Fatal("independent destination borrowed source nominal descriptors")
	}
}

func TestBackendMemoryNominalControlOwnership(t *testing.T) {
	source := engine.New()
	sourceView := source.CatalogRead()
	sourceRow, sourceArray := backendMemoryNominalPair(t, sourceView)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), source, &dump); err != nil { t.Fatal(err) }
	destination := engine.New()
	if err := engine.Restore(context.Background(), destination, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	firstRow, firstArray := backendMemoryNominalPair(t, destination.CatalogRead())
	for range 2 {
		var replay bytes.Buffer
		if err := engine.Dump(context.Background(), destination, &replay); err != nil { t.Fatal(err) }
		if !bytes.Equal(replay.Bytes(), dump.Bytes()) { t.Fatal("builtin completion changed logical replay") }
		if err := engine.Restore(context.Background(), destination, bytes.NewReader(replay.Bytes())); err != nil { t.Fatal(err) }
	}
	view := destination.CatalogRead()
	row, array := backendMemoryNominalPair(t, view)
	if row == firstRow || array == firstArray || row == sourceRow || array == sourceArray || row.CompositeMember(0).Usage.Type == sourceRow.CompositeMember(0).Usage.Type {
		t.Fatal("native replacement imported nominal source backing")
	}
	if oldRow, oldArray := backendMemoryNominalPair(t, sourceView); oldRow != sourceRow || oldArray != sourceArray { t.Fatal("source backing mutated") }
	// Exercise the actual copy-on-change/importing traversal independently of
	// logical replay: copied primitives require both nominal codecs to follow.
	for _, importing := range []bool{false, true} {
		state := catalog.CloneState(view.State())
		textAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}
		textOwner := *view.MustType(textAddress)
		textGeneration := *textOwner.SQLType
		textOwner.SQLType = &textGeneration
		state.SetDescriptor(textAddress, &textOwner)
		changed, err := typedef.ReownDomainTypes(state, nil, importing, routine.RangeTypeRebinder(state, nil), nil)
		if err != nil || !changed { t.Fatal("builtin nominal reownership", importing, changed, err) }
		if err := stored.ReownScalarTypes(state, nil, nil); err != nil { t.Fatal(err) }
		next := destination.Catalog.Read(state)
		nextRow, nextArray := backendMemoryNominalPair(t, next)
		if nextRow == row || nextArray == array || nextRow.CompositeMember(0).Usage.Type != textOwner.SQLType || nextArray.Element() != nextRow {
			t.Fatal("reownership lost current builtin member/companion authority", importing)
		}
		if oldRow, oldArray := backendMemoryNominalPair(t, view); oldRow != row || oldArray != array { t.Fatal("reownership mutated published descriptors") }
	}
}
