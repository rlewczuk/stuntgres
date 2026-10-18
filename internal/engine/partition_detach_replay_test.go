package engine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestPartitionDetachReplayEvents(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "real_backing_after_detach", true: "childless_collision_reserved"}[empty], func(t *testing.T) {
			f := partitionConstraintDescriptors(t)
			f.child.sqlIndex.partitionParent = f.parent.address
			if empty {
				f.root.partition.children = nil
				f.root.partition.defaultChild = ids.ObjectAddress{}
				f.leaf.membership = nil
				f.child.sqlIndex.partitionParent = ids.ObjectAddress{}
			}
			reserved := ids.NameKey{ClassID: ids.RelationClassID, Namespace: f.root.namespace, Name: partitionReplayStem + "1"}
			f.view.state.names[reserved] = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46700}
			r, c, pi, ci := f.root.address.ObjectID, f.leaf.address.ObjectID, f.parent.address.ObjectID, f.child.address.ObjectID
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
			if f.parent.sqlIndex.valid || f.child.sqlIndex.partitionParent != (map[bool]ids.ObjectAddress{false: f.parent.address, true: {}}[empty]) {
				t.Fatal("renderer mutated captured definitions")
			}
		})
	}
}

func TestPartitionDetachReplayLocalEvents(t *testing.T) {
	f := partitionConstraintDescriptors(t)
	f.child.sqlIndex.partitionParent = f.parent.address
	f.parent.sqlIndex.include = nil
	f.child.sqlIndex.include = nil
	for _, index := range [2]*relationDescriptor{f.parent, f.child} {
		index.columns = index.columns[:2]
		index.attributes = index.attributes[:2]
		index.attributeHighWater = 2
		index.index = map[string]int{"a": 0, "b": 1}
		index.sqlIndex.keys[1].desc = false
		index.sqlIndex.keys[1].nullsFirst = false
	}
	local, definition := *f.child, *f.child.sqlIndex
	local.address.ObjectID = 19999
	local.name = "unit_local_older"
	definition.partitionParent = ids.ObjectAddress{}
	constraint := uniqueConstraint{OID: 46900, Name: local.name, Index: local.address, Attributes: []int32{22, 11}, IsLocal: true}
	definition.constraint = uniqueBacking(&constraint)
	local.sqlIndex = &definition
	f.leaf.uniqueConstraints = []uniqueConstraint{constraint}
	f.view.state.descriptors[local.address] = &local
	f.view.state.names[descriptorNameKey(&local)] = local.address
	if !partitionReplayUsingIndex(f.view, &local) || usingIndexDump(&local) {
		t.Fatal("local origin must be explicit only for this captured replay")
	}
	r, c, pi, ci, li := f.root.address.ObjectID, f.leaf.address.ObjectID, f.parent.address.ObjectID, f.child.address.ObjectID, local.address.ObjectID
	regclass := f.view.state.descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}].(*typeDescriptor).typ
	consumer := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46901}, namespace: f.leaf.namespace, owner: f.leaf.owner, name: "unit_local_consumer", revision: 1, relationKind: tableRelation, columns: []ColumnDescriptor{{Name: "ref", Usage: regclass.Usage()}}, attributes: []int32{1}, attributeHighWater: 1, index: map[string]int{"ref": 0}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.address.ObjectID), 10))
	consumer.defaults = map[int32]columnDefault{1: {OID: 46902, expression: literalExpression{expressionInfo: expressionInfo{usage: regclass.Usage()}, value: value}, dependencies: []ids.ObjectRef{f.e.domain.Ref(f.view.Epoch(), local.address)}}}
	f.view.state.descriptors[consumer.address] = consumer
	f.view.state.names[descriptorNameKey(consumer)] = consumer.address
	provider, err := dumpPredecessor(f.view, consumer.defaults[1].dependencies[0].Address, consumer.address)
	if err != nil {
		t.Fatal(err)
	}
	graph := map[dumpEventKey][]dumpEventKey{{oid: r}: nil, {oid: c}: {{oid: r}}, {oid: pi}: {{oid: r}}, {oid: ci}: {{oid: c}}, {oid: li}: {{oid: c}}, {oid: consumer.address.ObjectID}: {{oid: provider}}}
	declarations, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A real captured private-name projection exercises the local constraint's
	// rename barrier. Stored REGCLASS data must wait for that original identity.
	tables := []dumpRelation{{descriptor: f.root}, {descriptor: f.leaf}, {descriptor: consumer, columns: consumer.columns, attributes: consumer.attributes, defaults: consumer.defaults, versions: []rowVersion{{row: 1, version: 1, values: []sqltype.Value{value}}}}}
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
	rows := dumpEventKey{oid: consumer.address.ObjectID, phase: dumpRows}
	if position(dumpEventKey{oid: consumer.address.ObjectID}) >= position(rename) {
		t.Fatal("private-name default declaration lost its backing before binding", order)
	}
	if position(dumpEventKey{oid: li}) >= position(rename) || position(rename) >= position(rows) {
		t.Fatal("consumer replay precedes its own local index identity", order)
	}
	if f.leaf.uniqueConstraints[0].OID != constraint.OID || local.sqlIndex.partitionParent != (ids.ObjectAddress{}) || f.child.sqlIndex.partitionParent != f.parent.address {
		t.Fatal("scheduler changed source ownership")
	}
}
