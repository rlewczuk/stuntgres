package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func TestPartitionAttachCheckReplayAuthority(t *testing.T) {
	source := New()
	e := source.restoreStage()
	parent := partitionAttachUnitRoot(t, e, "replay_parent", 34000)
	child := inheritanceUnitRelation(e, "replay_child", 34001, "k")
	child.RelationKind = catalog.TableRelation
	child.Membership = &catalog.PartitionMembership{Parent: parent.Address(), DefaultBound: true}
	parent.Partition.Children = []ids.ObjectAddress{child.Address()}
	parent.Partition.DefaultChild = child.Address()
	partitionAttachUnitInstall(t, e, parent, child)
	child = e.CatalogRead().MustRelation(child.Address())
	op, done := roleOperation(t, e, "postgres")
	defer done()
	raw := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: child.Name()}}}, Actions: []parser.AlterTableAction{{Action: parser.AddCheck, Check: &parser.CheckConstraint{Name: parser.Identifier{Text: "local"}, NotValid: true, Expression: parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}}}}}
	if err := e.beginPartitionCheckReplay(op, raw); err != nil {
		t.Fatal(err)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if operationReplayOrZero(op).partitionCheck != child || !partitionCheckReplayAllowed(op, auth, raw, child) {
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
	foreign.Catalog = source.CatalogRead()
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
	operationReplay(op).partitionCheck = &virtual
	if partitionCheckReplayAllowed(op, auth, raw, &virtual) {
		t.Fatal("virtual CHECK granted")
	}
	operationReplay(op).partitionCheck = nil
	if err := partitionConsumerBoundary(op, auth, raw, child); err == nil {
		t.Fatal("public hierarchy ADD CHECK admitted")
	}
	publicOp, publicDone := roleOperation(t, source, "postgres")
	defer publicDone()
	if err := source.beginPartitionCheckReplay(publicOp, raw); err == nil {
		t.Fatal("public bootstrap forged private import")
	}
	operationReplay(op).partitionCheck = child
	done()
	if partitionCheckReplayAllowed(op, auth, raw, child) {
		t.Fatal("ended lease retained capability")
	}
}
