package engine

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartitionDetachUsingIndexReplayAuthority(t *testing.T) {
	source := New()
	e := source.restoreStage()
	parent := partitionAttachUnitRoot(t, e, "using_replay_parent", 46800)
	child := inheritanceUnitRelation(e, "using_replay_child", 46801, "k")
	child.RelationKind = catalog.TableRelation
	child.Membership = &catalog.PartitionMembership{Parent: parent.Address(), DefaultBound: true}
	parent.Partition.Children = []ids.ObjectAddress{child.Address()}
	parent.Partition.DefaultChild = child.Address()
	class, _ := sqltype.DefaultIndexClass(child.Columns[0].Usage.Type)
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 46802}, ObjectNamespace: child.Namespace(), ObjectName: "using_replay_local", ObjectRevision: 1, Owner: child.Owner, RelationKind: catalog.IndexRelation, Columns: slices.Clone(child.Columns), Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, Index: map[string]int{"k": 0}, SQLIndex: &catalog.IndexDefinition{Parent: child.Address(), Method: catalog.BtreeMethod, Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}, Unique: true, Ready: true, Valid: true, Live: true}}
	index.Columns[0].OriginTable = uint32(index.Address().ObjectID)
	index.Columns[0].OriginAttribute = 1
	child.History.HasIndexes = true
	partitionAttachUnitInstall(t, e, parent, child, index)
	child = e.catalog.state.MustRelation(child.Address())
	index = e.catalog.state.MustRelation(index.Address())
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: child.Name()}}}, Actions: []parser.AlterTableAction{{Action: parser.AddUsingIndex, UsingIndex: &parser.UsingIndexConstraint{Name: parser.Identifier{Text: index.Name()}, Index: parser.Identifier{Text: index.Name()}}}}}
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
	next := catalog.CloneState(e.catalog.state)
	changed := *index
	changed.ObjectRevision++
	next.SetDescriptor(index.Address(), &changed)
	e.catalog.state = next
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
