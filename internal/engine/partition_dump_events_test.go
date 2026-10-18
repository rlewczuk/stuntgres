package engine

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPartitionAttachDumpPhaseDependencies(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	regclass := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).typ
	for _, table := range []*relationDescriptor{f.root, f.leaf} {
		table.columns = append(slices.Clone(table.columns), ColumnDescriptor{Name: "ref", Usage: regclass.Usage(), Nullable: true})
		table.attributes = append(slices.Clone(table.attributes), 99)
		table.attributeHighWater = 99
	}
	other := *f.leaf
	other.address.ObjectID = 90001
	other.name = "unit_other"
	other.membership = &partitionMembership{parent: f.root.address}
	f.root.partition.children = []ids.ObjectAddress{other.address, f.leaf.address}
	f.child.sqlIndex.partitionParent = f.parent.address
	good, goodDefinition := *f.parent, *f.parent.sqlIndex
	good.address.ObjectID = 90002
	good.name = "unit_good"
	good.sqlIndex = &goodDefinition
	goodDefinition.valid = true
	leafGood, leafDefinition := *f.child, *f.child.sqlIndex
	leafGood.address.ObjectID = 90003
	leafGood.name = "unit_leaf_good"
	leafGood.sqlIndex = &leafDefinition
	leafDefinition.partitionParent = good.address
	otherGood, otherDefinition := leafGood, leafDefinition
	otherGood.address.ObjectID = 90004
	otherGood.name = "unit_other_good"
	otherGood.sqlIndex = &otherDefinition
	otherDefinition.parent = other.address
	for _, d := range []*relationDescriptor{&other, &good, &leafGood, &otherGood} {
		f.view.state.setDescriptor(d.address, d)
		f.view.state.bindName(descriptorNameKey(d), d.address)
	}
	boolean := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)}).typ
	f.leaf.checkConstraints = []checkConstraint{{OID: 90005, Name: "unit_late", NotValid: true, expression: literalExpression{Info: expressionInfo{Usage: boolean.Usage()}, value: sqltype.Boolean(boolean, false)}}}
	values := make([]sqltype.Value, len(f.leaf.columns))
	for i, column := range f.leaf.columns {
		values[i] = sqltype.Null(column.Usage.Type)
	}
	for _, reference := range []*relationDescriptor{f.parent, &good} {
		t.Run(reference.name, func(t *testing.T) {
			rowValues := slices.Clone(values)
			rowValues[len(rowValues)-1] = sqltype.String(regclass, strconv.FormatUint(uint64(reference.address.ObjectID), 10))
			tables := []dumpRelation{{descriptor: f.root}, {descriptor: &other}, {descriptor: f.leaf, columns: f.leaf.columns, attributes: f.leaf.attributes, versions: []rowVersion{{row: 1, version: 1, values: rowValues}}}}
			graph := make(map[dumpEventKey][]dumpEventKey)
			indices := make(map[ids.OID]int)
			for _, table := range tables {
				graph[dumpEventKey{oid: table.descriptor.address.ObjectID}] = nil
			}
			for _, index := range []*relationDescriptor{f.parent, f.child, &good, &leafGood, &otherGood} {
				graph[dumpEventKey{oid: index.address.ObjectID}] = []dumpEventKey{{oid: index.sqlIndex.parent.ObjectID}}
				indices[index.address.ObjectID] = len(indices)
			}
			declarations, err := viewDumpOrder(context.Background(), graph, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, replay := partitionConstraintDumpCatalog(f.view)
			if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			order, err := viewDumpOrder(context.Background(), graph, map[ids.OID]ids.OID{good.address.ObjectID: 1})
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
			otherMember := dumpEventKey{oid: other.address.ObjectID, phase: dumpTableAttachment}
			leafMember := dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpTableAttachment}
			before(otherMember, dumpEventKey{oid: f.parent.address.ObjectID})
			before(dumpEventKey{oid: f.parent.address.ObjectID}, leafMember)
			before(otherMember, dumpEventKey{oid: good.address.ObjectID})
			before(leafMember, dumpEventKey{oid: good.address.ObjectID})
			before(dumpEventKey{oid: reference.address.ObjectID}, dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpRows})
			before(dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpRows}, dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpInvalidConstraints})
			if reference == f.parent {
				before(dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpInvalidConstraints}, leafMember)
			} else {
				before(leafMember, dumpEventKey{oid: f.leaf.address.ObjectID, phase: dumpRows})
			}
			for _, child := range []*relationDescriptor{f.child, &leafGood, &otherGood} {
				before(dumpEventKey{oid: child.address.ObjectID, phase: dumpIndexAttachment}, dumpEventKey{oid: f.root.address.ObjectID, phase: dumpTableCompletion})
			}
			if f.parent.sqlIndex.valid || f.child.sqlIndex.partitionParent != f.parent.address || !good.sqlIndex.valid || leafGood.sqlIndex.partitionParent != good.address || otherGood.sqlIndex.partitionParent != good.address {
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
