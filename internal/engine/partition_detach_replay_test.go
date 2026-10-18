package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPartitionDetachReplayEvents(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "real_backing_after_detach", true: "childless_collision_reserved"}[empty], func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			f.child.SQLIndex.PartitionParent = f.parent.Address()
			if empty {
				f.root.Partition.Children = nil
				f.root.Partition.DefaultChild = ids.ObjectAddress{}
				f.leaf.Membership = nil
				f.child.SQLIndex.PartitionParent = ids.ObjectAddress{}
			}
			reserved := ids.NameKey{ClassID: ids.RelationClassID, Namespace: f.root.Namespace(), Name: partitionReplayStem + "1"}
			f.view.State().BindName(reserved, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46700})
			r, c, pi, ci := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID
			graph := map[dumpEventKey][]dumpEventKey{{oid: r}: nil, {oid: c}: nil, {oid: pi}: {{oid: r}}, {oid: ci}: {{oid: c}}}
			if !empty {
				graph[dumpEventKey{oid: c}] = []dumpEventKey{{oid: r}}
			}
			declarations := []dumpEventKey{{oid: r}, {oid: c}, {oid: pi}, {oid: ci}}
			tables := []dumpRelation{{descriptor: f.root}, {descriptor: f.leaf}}
			indices := map[ids.OID]int{pi: 0, ci: 1}
			replay := &partitionConstraintReplay{original: f.view, temporary: map[ids.ObjectAddress]string{}}
			if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
				t.Fatal(err)
			}
			plans := partitionInvalidDumpEvents(f.view, graph, indices)
			order, err := viewDumpOrder(context.Background(), graph, nil)
			if err != nil {
				t.Fatal("replay introduced cycle", err)
			}
			position := func(event dumpEventKey) int {
				for i, candidate := range order {
					if candidate == event {
						return i
					}
				}
				t.Fatal("missing event", event)
				return -1
			}
			prepare := dumpEventKey{oid: r, phase: dumpInvalidIndexPreparation}
			finish := dumpEventKey{oid: r, phase: dumpInvalidIndexCompletion}
			if position(prepare) >= position(finish) || position(finish) >= position(dumpEventKey{oid: r, phase: dumpTableCompletion}) {
				t.Fatal("teardown/reattach not a completion predecessor", order)
			}
			if empty {
				if plans[r].scratch != partitionReplayStem+"2" || position(prepare) >= position(dumpEventKey{oid: pi}) {
					t.Fatal("scratch collision/declaration ordering", plans[r], order)
				}
				text, err := plans[r].sql(context.Background(), f.view, dumpInvalidIndexCompletion)
				if err != nil || !strings.Contains(text, "DETACH PARTITION") || !strings.Contains(text, "DROP TABLE") {
					t.Fatal("missing real teardown", text, err)
				}
			} else {
				if plans[r].child != f.leaf || position(prepare) >= position(dumpEventKey{oid: ci}) || position(dumpEventKey{oid: ci}) >= position(finish) {
					t.Fatal("real backing must build standalone between detach and table reattach", order)
				}
				if _, exists := graph[dumpEventKey{oid: ci, phase: dumpIndexAttachment}]; exists {
					t.Fatal("explicit completion would promote preserved invalid parent")
				}
				text, err := plans[r].sql(context.Background(), f.view, dumpInvalidIndexCompletion)
				if err != nil || !strings.Contains(text, "ATTACH PARTITION") || !strings.Contains(text, "DEFAULT") {
					t.Fatal("lost captured bound", text, err)
				}
			}
			if f.parent.SQLIndex.Valid || f.child.SQLIndex.PartitionParent != (map[bool]ids.ObjectAddress{false: f.parent.Address(), true: {}}[empty]) {
				t.Fatal("renderer mutated captured definitions")
			}
		})
	}
}

func TestPartitionDetachReplayLocalEvents(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.child.SQLIndex.PartitionParent = f.parent.Address()
	f.parent.SQLIndex.Include = nil
	f.child.SQLIndex.Include = nil
	for _, index := range [2]*relationDescriptor{f.parent, f.child} {
		index.Columns = index.Columns[:2]
		index.Attributes = index.Attributes[:2]
		index.AttributeHighWater = 2
		index.Index = map[string]int{"a": 0, "b": 1}
		index.SQLIndex.Keys[1].Desc = false
		index.SQLIndex.Keys[1].NullsFirst = false
	}
	local, definition := *f.child, *f.child.SQLIndex
	local.ObjectAddress.ObjectID = 19999
	local.ObjectName = "unit_local_older"
	definition.PartitionParent = ids.ObjectAddress{}
	constraint := uniqueConstraint{OID: 46900, Name: local.Name(), Index: local.Address(), Attributes: []int32{22, 11}, IsLocal: true}
	definition.Constraint = uniqueBacking(&constraint)
	local.SQLIndex = &definition
	f.leaf.UniqueConstraints = []uniqueConstraint{constraint}
	f.view.State().SetDescriptor(local.Address(), &local)
	f.view.State().BindName(descriptorNameKey(&local), local.Address())
	if !partitionReplayUsingIndex(f.view, &local) || usingIndexDump(&local) {
		t.Fatal("local origin must be explicit only for this captured replay")
	}
	r, c, pi, ci, li := f.root.Address().ObjectID, f.leaf.Address().ObjectID, f.parent.Address().ObjectID, f.child.Address().ObjectID, local.Address().ObjectID
	regclass := f.view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).SQLType
	consumer := &relationDescriptor{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46901}, ObjectNamespace: f.leaf.Namespace(), Owner: f.leaf.Owner, ObjectName: "unit_local_consumer", ObjectRevision: 1, RelationKind: tableRelation, Columns: []ColumnDescriptor{{Name: "ref", Usage: regclass.Usage()}}, Attributes: []int32{1}, AttributeHighWater: 1, Index: map[string]int{"ref": 0}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.Address().ObjectID), 10))
	consumer.Defaults = map[int32]columnDefault{1: {OID: 46902, Expression: literalExpression{Info: expr.Info{Usage: regclass.Usage()}, value: value}, Dependencies: []ids.ObjectRef{f.e.catalog.Domain.Ref(f.view.Epoch(), local.Address())}}}
	f.view.State().SetDescriptor(consumer.Address(), consumer)
	f.view.State().BindName(descriptorNameKey(consumer), consumer.Address())
	provider, err := dumpPredecessor(f.view, consumer.Defaults[1].Dependencies[0].Address, consumer.Address())
	if err != nil {
		t.Fatal(err)
	}
	graph := map[dumpEventKey][]dumpEventKey{{oid: r}: nil, {oid: c}: {{oid: r}}, {oid: pi}: {{oid: r}}, {oid: ci}: {{oid: c}}, {oid: li}: {{oid: c}}, {oid: consumer.Address().ObjectID}: {{oid: provider}}}
	declarations, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A real captured private-name projection exercises the local constraint's
	// rename barrier. Stored REGCLASS data must wait for that original identity.
	tables := []dumpRelation{{descriptor: f.root}, {descriptor: f.leaf}, {descriptor: consumer, columns: consumer.Columns, attributes: consumer.Attributes, defaults: consumer.Defaults, versions: []rowVersion{{row: 1, version: 1, values: []sqltype.Value{value}}}}}
	indices := map[ids.OID]int{pi: 0, ci: 1, li: 2}
	_, replay := partitionConstraintDumpCatalog(f.view)
	if err := partitionDumpEvents(context.Background(), f.view, graph, declarations, tables, indices, replay, false); err != nil {
		t.Fatal(err)
	}
	partitionInvalidDumpEvents(f.view, graph, indices)
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal("deferred local origin created cycle", err)
	}
	position := func(event dumpEventKey) int {
		for i, candidate := range order {
			if candidate == event {
				return i
			}
		}
		t.Fatal("missing event", event)
		return -1
	}
	prepare := dumpEventKey{oid: r, phase: dumpInvalidIndexPreparation}
	finish := dumpEventKey{oid: r, phase: dumpInvalidIndexCompletion}
	if position(prepare) >= position(dumpEventKey{oid: ci}) || position(dumpEventKey{oid: ci}) >= position(finish) || position(finish) >= position(dumpEventKey{oid: li}) {
		t.Fatal("older local index can steal captured backing", order)
	}
	rename := dumpEventKey{oid: li, phase: dumpRename}
	rows := dumpEventKey{oid: consumer.Address().ObjectID, phase: dumpRows}
	if position(dumpEventKey{oid: consumer.Address().ObjectID}) >= position(rename) {
		t.Fatal("private-name default declaration lost its backing before binding", order)
	}
	if position(dumpEventKey{oid: li}) >= position(rename) || position(rename) >= position(rows) {
		t.Fatal("consumer replay precedes its own local index identity", order)
	}
	if f.leaf.UniqueConstraints[0].OID != constraint.OID || local.SQLIndex.PartitionParent != (ids.ObjectAddress{}) || f.child.SQLIndex.PartitionParent != f.parent.Address() {
		t.Fatal("scheduler changed source ownership")
	}
}
