package engine

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachDumpPhaseDependencies(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	regclass := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).SQLType
	for _, table := range []*catalog.Relation{f.root, f.leaf} {
		table.Columns = append(slices.Clone(table.Columns), catalog.Column{Name: "ref", Usage: regclass.Usage(), Nullable: true})
		table.Attributes = append(slices.Clone(table.Attributes), 99)
		table.ColumnMeta.HighWater = 99
	}
	other := *f.leaf
	other.ObjectAddress.ObjectID = 90001
	other.ObjectName = "unit_other"
	other.Membership = &catalog.PartitionMembership{Parent: f.root.Address()}
	f.root.Partition.Children = []ids.ObjectAddress{other.Address(), f.leaf.Address()}
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	good, goodDefinition := *f.parent, *f.parent.SQLIndex
	good.ObjectAddress.ObjectID = 90002
	good.ObjectName = "unit_good"
	good.SQLIndex = &goodDefinition
	goodDefinition.Valid = true
	leafGood, leafDefinition := *f.child, *f.child.SQLIndex
	leafGood.ObjectAddress.ObjectID = 90003
	leafGood.ObjectName = "unit_leaf_good"
	leafGood.SQLIndex = &leafDefinition
	leafDefinition.PartitionParent = good.Address()
	otherGood, otherDefinition := leafGood, leafDefinition
	otherGood.ObjectAddress.ObjectID = 90004
	otherGood.ObjectName = "unit_other_good"
	otherGood.SQLIndex = &otherDefinition
	otherDefinition.Parent = other.Address()
	for _, d := range []*catalog.Relation{&other, &good, &leafGood, &otherGood} {
		f.view.State().SetDescriptor(d.Address(), d)
		f.view.State().BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	boolean := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)}).SQLType
	f.leaf.Constraints.Check = []catalog.CheckConstraint{{OID: 90005, Name: "unit_late", NotValid: true, Expression: plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}}
	values := make([]sqltype.Value, len(f.leaf.Columns))
	for i, column := range f.leaf.Columns {
		values[i] = sqltype.Null(column.Usage.Type)
	}
	for _, reference := range []*catalog.Relation{f.parent, &good} {
		t.Run(reference.Name(), func(t *testing.T) {
			rowValues := slices.Clone(values)
			rowValues[len(rowValues)-1] = sqltype.String(regclass, strconv.FormatUint(uint64(reference.Address().ObjectID), 10))
			tables := []table.DumpRelation{{Descriptor: f.root}, {Descriptor: &other}, {Descriptor: f.leaf, Columns: f.leaf.Columns, Attributes: f.leaf.Attributes, Versions: []storage.RowVersion{{Row: 1, Version: 1, Values: rowValues}}}}
			graph := make(dump.Graph)
			indices := make(map[ids.OID]int)
			for _, table := range tables {
				graph[dump.Event{OID: table.Descriptor.Address().ObjectID}] = nil
			}
			for _, index := range []*catalog.Relation{f.parent, f.child, &good, &leafGood, &otherGood} {
				graph[dump.Event{OID: index.Address().ObjectID}] = []dump.Event{{OID: index.SQLIndex.Parent.ObjectID}}
				indices[index.Address().ObjectID] = len(indices)
			}
			declarations, err := dump.Order(context.Background(), graph, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, replay := partitionConstraintDumpCatalog(dumpResolver{}, f.view)
			if err := partitionDumpEvents(context.Background(), dumpResolver{}, f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			order, err := dump.Order(context.Background(), graph, map[ids.OID]ids.OID{good.Address().ObjectID: 1})
			if err != nil {
				t.Fatal(err)
			}
			before := func(first, second dump.Event) {
				t.Helper()
				a, b := slices.Index(order, first), slices.Index(order, second)
				if a < 0 || b < 0 || a >= b {
					t.Fatalf("required predecessor %v before %v: %v", first, second, order)
				}
			}
			otherMember := dump.Event{OID: other.Address().ObjectID, Phase: dump.TableAttachment}
			leafMember := dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.TableAttachment}
			before(otherMember, dump.Event{OID: f.parent.Address().ObjectID})
			before(dump.Event{OID: f.parent.Address().ObjectID}, leafMember)
			before(otherMember, dump.Event{OID: good.Address().ObjectID})
			before(leafMember, dump.Event{OID: good.Address().ObjectID})
			before(dump.Event{OID: reference.Address().ObjectID}, dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.Rows})
			before(dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.Rows}, dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.InvalidConstraints})
			if reference == f.parent {
				before(dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.InvalidConstraints}, leafMember)
			} else {
				before(leafMember, dump.Event{OID: f.leaf.Address().ObjectID, Phase: dump.Rows})
			}
			for _, child := range []*catalog.Relation{f.child, &leafGood, &otherGood} {
				before(dump.Event{OID: child.Address().ObjectID, Phase: dump.IndexAttachment}, dump.Event{OID: f.root.Address().ObjectID, Phase: dump.TableCompletion})
			}
			if f.parent.SQLIndex.Valid || f.child.SQLIndex.PartitionParent != f.parent.Address() || !good.SQLIndex.Valid || leafGood.SQLIndex.PartitionParent != good.Address() || otherGood.SQLIndex.PartitionParent != good.Address() {
				t.Fatal("phase construction changed the captured incomplete or valid index tree")
			}
		})
	}
}
