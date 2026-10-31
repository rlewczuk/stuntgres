package enginetests

import (
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestClusterUtilityClassification(t *testing.T) {
	for _, c := range []*parser.Cluster{{}, {Table: parser.ObjectName{Parts: []parser.Identifier{{Text: "t"}}}}} {
		if session.SnapshotStatement(c) || session.SnapshotTargetAdmission(c) || session.ReplacementRead(c) || session.DescribeBeforeSnapshot(c) != session.DescribeOperation || session.ModifyingTag(c) != "CLUSTER" || !session.ReadOnlyExempt(c) {
			t.Fatal("CLUSTER admission classification", c)
		}
		if err := session.ReadOnlyRefusal(c, true); err != nil {
			t.Fatal(err)
		}
		if session.TransactionalSchemaUtility(c) != (len(c.Table.Parts) != 0) {
			t.Fatal("named versus bare journal ownership", c)
		}
		if _, eligible := session.EventCommand(c); eligible {
			t.Fatal("CLUSTER fabricated a DDL event")
		}
		if tag, counted, insert, ok := session.ClassifyCommandTag(c, session.TxIdle); tag != "CLUSTER" || counted || insert || !ok {
			t.Fatal(tag, counted, insert, ok)
		}
	}
}
