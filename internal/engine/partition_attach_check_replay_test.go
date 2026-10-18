package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestPartitionAttachCheckReplayAuthority(t *testing.T) {
	source := New()
	e := source.restoreStage()
	parent := partitionAttachUnitRoot(t, e, "replay_parent", 34000)
	child := inheritanceUnitRelation(e, "replay_child", 34001, "k")
	child.RelationKind = tableRelation
	child.Membership = &partitionMembership{parent: parent.address, defaultBound: true}
	parent.Partition.children = []ids.ObjectAddress{child.address}
	parent.Partition.defaultChild = child.address
	partitionAttachUnitInstall(t, e, parent, child)
	child = e.CatalogRead().MustRelation(child.address)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: child.name}}}, Actions: []parser.AlterTableAction{{Action: parser.AddCheck, Check: &parser.CheckConstraint{Name: parser.Identifier{Text: "local"}, NotValid: true, Expression: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}}}}
	if err := e.beginPartitionCheckReplay(op, raw); err != nil {
		t.Fatal(err)
	}
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if op.restorePartitionCheck != child || !partitionCheckReplayAllowed(op, auth, raw, child) {
		t.Fatal("genuine leased physical replay rejected")
	}
	if err := partitionConsumerBoundary(op, auth, raw, child); err != nil {
		t.Fatal(err)
	}
	copyChild := *child
	if partitionCheckReplayAllowed(op, auth, raw, &copyChild) {
		t.Fatal("lookalike descriptor granted")
	}
	copyOp := *op
	if partitionCheckReplayAllowed(&copyOp, auth, raw, child) {
		t.Fatal("copied operation granted")
	}
	foreign := auth
	foreign.catalog = source.CatalogRead()
	if partitionCheckReplayAllowed(op, foreign, raw, child) {
		t.Fatal("foreign catalog granted")
	}
	valid := *raw
	action := raw.Actions[0]
	check := *action.Check
	check.NotValid = false
	action.Check = &check
	valid.Actions = []parser.AlterTableAction{action}
	if partitionCheckReplayAllowed(op, auth, &valid, child) {
		t.Fatal("valid CHECK granted")
	}
	virtual := *child
	virtual.Partition = parent.Partition
	op.restorePartitionCheck = &virtual
	if partitionCheckReplayAllowed(op, auth, raw, &virtual) {
		t.Fatal("virtual CHECK granted")
	}
	op.restorePartitionCheck = nil
	if err := partitionConsumerBoundary(op, auth, raw, child); err == nil {
		t.Fatal("public hierarchy ADD CHECK admitted")
	}
	publicOp, publicDone := roleOperation(t, source, "postgres")
	defer publicDone()
	if err := source.beginPartitionCheckReplay(publicOp, raw); err == nil {
		t.Fatal("public bootstrap forged private import")
	}
	op.restorePartitionCheck = child
	done()
	if partitionCheckReplayAllowed(op, auth, raw, child) {
		t.Fatal("ended lease retained capability")
	}
}
