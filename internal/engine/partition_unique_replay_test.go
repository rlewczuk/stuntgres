package engine

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func partitionUniqueReplayUnit(t *testing.T, primary bool) (*Engine, *relationDescriptor, *relationDescriptor, *parser.AlterTable) {
	t.Helper()
	e := New().restoreStage()
	root := partitionAttachUnitRoot(t, e, "unique_replay_root", 47200)
	mid := partitionAttachUnitRoot(t, e, "unique_replay_mid", 47201)
	leaf := inheritanceUnitRelation(e, "unique_replay_leaf", 47202, "k")
	leaf.relationKind = tableRelation
	e.mu.Lock()
	for _, table := range [3]*relationDescriptor{root, mid, leaf} {
		table.columns[0].Nullable = false
		table.notNullConstraints = []notNullConstraint{{Name: table.name + "_nn", Attribute: 1}}
		table.hasIndexes = true
		// Stored NOT NULL facts need real catalog identities before validation,
		// just as the canonical CREATE producer assigns them.
		if err := e.assignDefinitionOIDsLocked(table); err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
	}
	e.mu.Unlock()
	root.partition.children = []ids.ObjectAddress{mid.address}
	root.partition.defaultChild = mid.address
	mid.membership = &partitionMembership{parent: root.address, defaultBound: true}
	mid.partition.children = []ids.ObjectAddress{leaf.address}
	mid.partition.defaultChild = leaf.address
	leaf.membership = &partitionMembership{parent: mid.address, defaultBound: true}
	class, _ := sqltype.DefaultIndexClass(mid.columns[0].Usage.Type)
	index := func(table *relationDescriptor, id ids.OID, name string, parent ids.ObjectAddress, valid bool) *relationDescriptor {
		columns := slices.Clone(table.columns)
		columns[0].OriginTable = uint32(id)
		columns[0].OriginAttribute = 1
		return &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, namespace: table.namespace, owner: table.owner, name: name, revision: 1, relationKind: indexRelation, columns: columns, attributes: []int32{1}, attributeHighWater: 1, index: map[string]int{"k": 0}, sqlIndex: &indexDefinition{parent: table.address, partitionParent: parent, method: btreeMethod, keys: []indexKey{{attribute: 1, class: class}}, unique: true, valid: valid, ready: true, live: true}}
	}
	ri := index(root, 47203, "unique_replay_root_plain", ids.ObjectAddress{}, false)
	mi := index(mid, 47204, "unique_replay_mid_plain", ri.address, true)
	li := index(leaf, 47205, "unique_replay_leaf_plain", mi.address, true)
	partitionAttachUnitInstall(t, e, root, mid, leaf, ri, mi, li)
	mid = e.catalog.state.MustRelation(mid.address)
	leaf = e.catalog.state.MustRelation(leaf.address)
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: mid.name}}}, Actions: []parser.AlterTableAction{{Action: parser.AddUnique, Unique: &parser.UniqueConstraint{Name: parser.Identifier{Text: "__stuntgres_partition_key_1"}, Columns: []parser.Identifier{{Text: "k"}}, Primary: primary}}}}
	return e, mid, leaf, raw
}

func TestPartitionDetachUniqueReplayAuthority(t *testing.T) {
	e, mid, leaf, raw := partitionUniqueReplayUnit(t, true)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	if err := e.beginPartitionUniqueReplay(op, raw); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if !partitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("exact origin missing")
	}
	copied := *op
	if partitionUniqueReplayAllowed(&copied, auth, raw, mid) {
		t.Fatal("copied lease authorized")
	}
	lookalike := *mid
	if partitionUniqueReplayAllowed(op, auth, raw, &lookalike) {
		t.Fatal("copied table authorized")
	}
	raw.Actions[0].Unique.Columns[0].Text = "other"
	if partitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("changed definition authorized")
	}
	raw.Actions[0].Unique.Columns[0].Text = "k"
	changed := *leaf
	changed.revision++
	e.catalog.state.descriptors[leaf.address] = &changed
	if partitionUniqueReplayAllowed(op, auth, raw, mid) {
		t.Fatal("changed subtree authorized")
	}
	e.catalog.state.descriptors[leaf.address] = leaf
	op.restorePartitionUnique = nil
	if partitionConsumerBoundary(op, auth, raw, mid) == nil {
		t.Fatal("public hierarchy ADD widened")
	}
}

func TestPartitionDetachUniqueReplayPublication(t *testing.T) {
	for _, primary := range []bool{false, true} {
		for _, cancel := range []bool{false, true} {
			t.Run(map[bool]string{false: "unique", true: "primary"}[primary]+map[bool]string{false: "/publish", true: "/cancel"}[cancel], func(t *testing.T) {
				e, mid, leaf, raw := partitionUniqueReplayUnit(t, primary)
				scope, op := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
				if err := e.beginPartitionUniqueReplay(op, raw); err != nil {
					t.Fatal(err)
				}
				if err := scope.acquireRelation(raw, true); err != nil {
					t.Fatal(err)
				}
				before := e.catalog.state
				e.mu.Lock()
				auth, err := e.statementAuthorizationLocked(op, raw)
				if err != nil {
					e.mu.Unlock()
					t.Fatal(err)
				}
				_, err = e.alterTableLocked(scope, op, auth, raw, 0)
				if err != nil {
					t.Fatal(err)
				}
				if e.catalog.state != before {
					t.Fatal("private staging published before Finish")
				}
				if cancel {
					op.cancel(context.Canceled)
				}
				err = scope.Finish(nil)
				op.Done()
				if cancel {
					if err == nil || e.catalog.state != before {
						t.Fatal("canceled origin published", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				current := e.catalog.state.MustRelation(mid.address)
				child := e.catalog.state.MustRelation(leaf.address)
				if len(current.uniqueConstraints) != 1 || len(child.uniqueConstraints) != 1 || child.uniqueConstraints[0].Parent != current.uniqueConstraints[0].OID {
					t.Fatal("missing real constraint ancestry")
				}
				rootIndex := e.catalog.state.MustRelation(current.uniqueConstraints[0].Index)
				childIndex := e.catalog.state.MustRelation(child.uniqueConstraints[0].Index)
				if !rootIndex.sqlIndex.valid || childIndex.sqlIndex.partitionParent != rootIndex.address || childIndex.sqlIndex.constraint.Index != child.uniqueConstraints[0].Index || childIndex.sqlIndex.constraint.Primary != primary {
					t.Fatal("wrong local backing tree")
				}
				if e.catalog.state.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47204}) != before.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47204}) || e.catalog.state.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47205}) != before.Get(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47205}) {
					t.Fatal("attached plain tree consumed")
				}
				if len(child.notNullConstraints) != len(leaf.notNullConstraints) || len(current.notNullConstraints) != len(mid.notNullConstraints) {
					t.Fatal("private PK added NN rollout")
				}
			})
		}
	}
}

func TestPartitionDetachUniqueReplayEvents(t *testing.T) {
	e, mid, leaf, raw := partitionUniqueReplayUnit(t, true)
	scope, op := partitionAttachUnitScope(t, e, sessionStart(t, e, nil))
	if err := e.beginPartitionUniqueReplay(op, raw); err != nil {
		t.Fatal(err)
	}
	if err := scope.acquireRelation(raw, true); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	if _, err = e.alterTableLocked(scope, op, auth, raw, 0); err != nil {
		t.Fatal(err)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	view := e.CatalogRead()
	mid = view.MustRelation(mid.address)
	leaf = view.MustRelation(leaf.address)
	local := view.MustRelation(mid.uniqueConstraints[0].Index)
	child := view.MustRelation(leaf.uniqueConstraints[0].Index)
	if partitionReplayUsingIndex(view, local) || partitionReplayVirtualUniqueRoot(view, child) != local {
		t.Fatal("virtual definition assigned a physical origin")
	}
	provider, err := dumpPredecessor(view, child.address, ids.ObjectAddress{})
	if err != nil || provider != local.address.ObjectID {
		t.Fatal("subordinate provider is not the real ADD", provider, err)
	}
	graph := make(map[dumpEventKey][]dumpEventKey)
	var tables []dumpRelation
	indices := make(map[ids.OID]int)
	for _, descriptor := range view.Descriptors() {
		r, ok := descriptor.(*relationDescriptor)
		if !ok || r.address.ObjectID < 47200 || r.address.ObjectID > 47205 {
			continue
		}
		if r.sqlIndex == nil {
			graph[dumpEventKey{oid: r.address.ObjectID}] = nil
			if r.membership != nil {
				graph[dumpEventKey{oid: r.address.ObjectID}] = []dumpEventKey{{oid: r.membership.parent.ObjectID}}
			}
			tables = append(tables, dumpRelation{descriptor: r})
		} else {
			graph[dumpEventKey{oid: r.address.ObjectID}] = []dumpEventKey{{oid: r.sqlIndex.parent.ObjectID}}
			indices[r.address.ObjectID] = len(indices)
		}
	}
	graph[dumpEventKey{oid: local.address.ObjectID}] = []dumpEventKey{{oid: mid.address.ObjectID}}
	indices[local.address.ObjectID] = len(indices)
	regclass := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}).typ
	consumer := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 47206}, namespace: mid.namespace, owner: mid.owner, name: "unique_replay_consumer", revision: 1, relationKind: tableRelation, columns: []ColumnDescriptor{{Name: "ref", Usage: regclass.Usage()}, {Name: "leaf_ref", Usage: regclass.Usage()}}, attributes: []int32{1, 2}, attributeHighWater: 2, index: map[string]int{"ref": 0, "leaf_ref": 1}}
	value := sqltype.String(regclass, strconv.FormatUint(uint64(local.address.ObjectID), 10))
	leafValue := sqltype.String(regclass, strconv.FormatUint(uint64(child.address.ObjectID), 10))
	state := *view.state
	state.descriptors = make(map[ids.ObjectAddress]Descriptor, view.DescriptorCount()+1)
	for address, descriptor := range view.Descriptors() {
		state.descriptors[address] = descriptor
	}
	state.descriptors[consumer.address] = consumer
	view.state = &state
	graph[dumpEventKey{oid: consumer.address.ObjectID}] = []dumpEventKey{{oid: local.address.ObjectID}}
	tables = append(tables, dumpRelation{descriptor: consumer, columns: consumer.columns, attributes: consumer.attributes, versions: []rowVersion{{row: 1, version: 1, values: []sqltype.Value{value, leafValue}}}})
	declarations, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, replay := partitionConstraintDumpCatalog(view)
	if err = partitionDumpEvents(context.Background(), view, graph, declarations, tables, indices, replay, false); err != nil {
		t.Fatal(err)
	}
	partitionInvalidDumpEvents(view, graph, indices)
	order, err := viewDumpOrder(context.Background(), graph, nil)
	if err != nil {
		t.Fatal("virtual definition closes initial membership cycle", err)
	}
	before := func(a, b dumpEventKey) {
		t.Helper()
		i, j := slices.Index(order, a), slices.Index(order, b)
		if i < 0 || j < 0 || i >= j {
			t.Fatal("wrong real definition/name lifetime", a, b, order)
		}
	}
	before(dumpEventKey{oid: 47200, phase: dumpInvalidIndexCompletion}, dumpEventKey{oid: local.address.ObjectID})
	before(dumpEventKey{oid: local.address.ObjectID}, dumpEventKey{oid: child.address.ObjectID, phase: dumpRename})
	before(dumpEventKey{oid: consumer.address.ObjectID}, dumpEventKey{oid: local.address.ObjectID, phase: dumpRename})
	before(dumpEventKey{oid: local.address.ObjectID, phase: dumpRename}, dumpEventKey{oid: consumer.address.ObjectID, phase: dumpRows})
	before(dumpEventKey{oid: child.address.ObjectID, phase: dumpRename}, dumpEventKey{oid: consumer.address.ObjectID, phase: dumpRows})
	if len(mid.notNullConstraints) != 1 || len(leaf.notNullConstraints) != 1 {
		t.Fatal("graph erased NN provenance")
	}
}
