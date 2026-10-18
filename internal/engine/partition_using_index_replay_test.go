package engine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartitionDetachUsingIndexReplayAuthority(t *testing.T) {
	source := New()
	e := source.restoreStage()
	parent := partitionAttachUnitRoot(t, e, "using_replay_parent", 46800)
	child := inheritanceUnitRelation(e, "using_replay_child", 46801, "k")
	child.relationKind = tableRelation
	child.membership = &partitionMembership{parent: parent.address, defaultBound: true}
	parent.partition.children = []ids.ObjectAddress{child.address}
	parent.partition.defaultChild = child.address
	class, _ := sqltype.DefaultIndexClass(child.columns[0].Usage.Type)
	index := &relationDescriptor{address: ids.ObjectAddress{ClassID: RelationClassID, ObjectID: 46802}, namespace: child.namespace, name: "using_replay_local", revision: 1, owner: child.owner, relationKind: indexRelation, columns: slices.Clone(child.columns), attributes: []int32{1}, attributeHighWater: 1, index: map[string]int{"k": 0}, sqlIndex: &indexDefinition{parent: child.address, method: btreeMethod, keys: []indexKey{{attribute: 1, class: class}}, unique: true, ready: true, valid: true, live: true}}
	index.columns[0].OriginTable = uint32(index.address.ObjectID)
	index.columns[0].OriginAttribute = 1
	child.hasIndexes = true
	partitionAttachUnitInstall(t, e, parent, child, index)
	child = e.state.descriptors[child.address].(*relationDescriptor)
	index = e.state.descriptors[index.address].(*relationDescriptor)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: child.name}}}, Actions: []parser.AlterTableAction{{Action: parser.AddUsingIndex, UsingIndex: &parser.UsingIndexConstraint{Name: parser.Identifier{Text: index.name}, Index: parser.Identifier{Text: index.name}}}}}
	if err := e.beginPartitionUsingIndexReplay(op, raw); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if !partitionUsingIndexReplayAllowed(op, auth, raw, child) || op.restorePartitionUsingIndex.index != index {
		t.Fatal("exact private physical/index origin not captured")
	}
	if err := partitionConsumerBoundary(op, auth, raw, child); err != nil {
		t.Fatal(err)
	}
	lookalike := *child
	if partitionUsingIndexReplayAllowed(op, auth, raw, &lookalike) {
		t.Fatal("copied target granted")
	}
	copied := *op
	if partitionUsingIndexReplayAllowed(&copied, auth, raw, child) {
		t.Fatal("copied lease granted")
	}
	foreign := auth
	foreign.catalog = source.CatalogRead()
	if partitionUsingIndexReplayAllowed(op, foreign, raw, child) {
		t.Fatal("foreign catalog granted")
	}
	other := *raw
	if partitionUsingIndexReplayAllowed(op, auth, &other, child) {
		t.Fatal("different syntax granted")
	}
	origin := op.restorePartitionUsingIndex
	op.restorePartitionUsingIndex = nil
	if err := partitionConsumerBoundary(op, auth, raw, child); err == nil {
		t.Fatal("public hierarchy ADD USING INDEX admitted")
	}
	op.restorePartitionUsingIndex = origin
	next := cloneCatalog(e.state)
	changed := *index
	changed.revision++
	next.descriptors[index.address] = &changed
	e.state = next
	fresh, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if partitionUsingIndexReplayAllowed(op, fresh, raw, child) {
		t.Fatal("changed index generation granted")
	}
	publicOp, publicDone := roleOperation(t, source, "postgres")
	defer publicDone()
	if err := source.beginPartitionUsingIndexReplay(publicOp, raw); err == nil {
		t.Fatal("public bootstrap forged import origin")
	}
	done()
	if partitionUsingIndexReplayAllowed(op, fresh, raw, child) {
		t.Fatal("ended lease granted")
	}
}
