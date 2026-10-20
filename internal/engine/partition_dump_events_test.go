package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
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
	f.leaf.Constraints.Check = []catalog.CheckConstraint{{OID: 90005, Name: "unit_late", NotValid: true, Expression: literalExpression{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}}}
	values := make([]sqltype.Value, len(f.leaf.Columns))
	for i, column := range f.leaf.Columns {
		values[i] = sqltype.Null(column.Usage.Type)
	}
	for _, reference := range []*catalog.Relation{f.parent, &good} {
		t.Run(reference.Name(), func(t *testing.T) {
			rowValues := slices.Clone(values)
			rowValues[len(rowValues)-1] = sqltype.String(regclass, strconv.FormatUint(uint64(reference.Address().ObjectID), 10))
			tables := []dumpRelation{{descriptor: f.root}, {descriptor: &other}, {descriptor: f.leaf, columns: f.leaf.Columns, attributes: f.leaf.Attributes, versions: []storage.RowVersion{{Row: 1, Version: 1, Values: rowValues}}}}
			graph := make(map[dumpEventKey][]dumpEventKey)
			indices := make(map[ids.OID]int)
			for _, table := range tables {
				graph[dumpEventKey{oid: table.descriptor.Address().ObjectID}] = nil
			}
			for _, index := range []*catalog.Relation{f.parent, f.child, &good, &leafGood, &otherGood} {
				graph[dumpEventKey{oid: index.Address().ObjectID}] = []dumpEventKey{{oid: index.SQLIndex.Parent.ObjectID}}
				indices[index.Address().ObjectID] = len(indices)
			}
			declarations, err := viewDumpOrder(context.Background(), graph, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, replay := partitionConstraintDumpCatalog(f.view)
			if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			order, err := viewDumpOrder(context.Background(), graph, map[ids.OID]ids.OID{good.Address().ObjectID: 1})
			if err != nil {
				t.Fatal(err)
			}
			before := func(first, second dumpEventKey) {
				t.Helper()
				a, b := slices.Index(order, first), slices.Index(order, second)
				if a < 0 || b < 0 || a >= b {
					t.Fatalf("required predecessor %v before %v: %v", first, second, order)
				}
			}
			otherMember := dumpEventKey{oid: other.Address().ObjectID, phase: dumpTableAttachment}
			leafMember := dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpTableAttachment}
			before(otherMember, dumpEventKey{oid: f.parent.Address().ObjectID})
			before(dumpEventKey{oid: f.parent.Address().ObjectID}, leafMember)
			before(otherMember, dumpEventKey{oid: good.Address().ObjectID})
			before(leafMember, dumpEventKey{oid: good.Address().ObjectID})
			before(dumpEventKey{oid: reference.Address().ObjectID}, dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpRows})
			before(dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpRows}, dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpInvalidConstraints})
			if reference == f.parent {
				before(dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpInvalidConstraints}, leafMember)
			} else {
				before(leafMember, dumpEventKey{oid: f.leaf.Address().ObjectID, phase: dumpRows})
			}
			for _, child := range []*catalog.Relation{f.child, &leafGood, &otherGood} {
				before(dumpEventKey{oid: child.Address().ObjectID, phase: dumpIndexAttachment}, dumpEventKey{oid: f.root.Address().ObjectID, phase: dumpTableCompletion})
			}
			if f.parent.SQLIndex.Valid || f.child.SQLIndex.PartitionParent != f.parent.Address() || !good.SQLIndex.Valid || leafGood.SQLIndex.PartitionParent != good.Address() || otherGood.SQLIndex.PartitionParent != good.Address() {
				t.Fatal("phase construction changed the captured incomplete or valid index tree")
			}
		})
	}
}

func TestPartitionAttachDumpPhaseScheduler(t *testing.T) {
	definition := dumpEventKey{oid: 90}
	membership := dumpEventKey{oid: 20, phase: dumpTableAttachment}
	data := dumpEventKey{oid: 20, phase: dumpRows}
	check := dumpEventKey{oid: 20, phase: dumpInvalidConstraints}
	graph := map[dumpEventKey][]dumpEventKey{definition: {membership}, membership: nil, data: {definition}, check: {data}}
	order, err := viewDumpOrder(context.Background(), graph, map[ids.OID]ids.OID{90: 1})
	if err != nil || !slices.Equal(order, []dumpEventKey{membership, definition, data, check}) {
		t.Fatal("source priority bypassed a membership/name/data/origin predecessor", order, err)
	}
	addDumpDependency(graph, membership, check)
	if _, err := viewDumpOrder(context.Background(), graph, nil); err == nil {
		t.Fatal("cyclic phase dependencies accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := viewDumpOrder(ctx, graph, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled phase scheduler", err)
	}
}
