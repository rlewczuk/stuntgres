package session

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestSecurityLabelPhases(t *testing.T) {
	stmt := &parser.SecurityLabel{}
	if DescribeBeforeSnapshot(stmt) != DescribeOperation || DescribeAfterSnapshot(stmt) || SnapshotStatement(stmt) || SnapshotTargetAdmission(stmt) || ReplacementRead(stmt) {
		t.Fatal("provider resolution must execute without target snapshots or replacement fences")
	}
	if ModifyingTag(stmt) != "SECURITY LABEL" || ReadOnlyExempt(stmt) || ExplicitUtility(stmt) || TransactionalSchemaUtility(stmt) || EarlyStandaloneUtility(stmt) {
		t.Fatal("modifying utility must reach provider resolution in a read-write transaction without staged changes")
	}
	if got := sqlText(ReadOnlyRefusal(stmt, true)); got != "25006 cannot execute SECURITY LABEL in a read-only transaction" { t.Fatal(got) }
	if err := ReadOnlyRefusal(stmt, false); err != nil { t.Fatal(err) }
	if PreparableBody(stmt) || ProtocolPlannedStatement(stmt) || ProtocolHasRows(stmt, nil) || AbortedControlAllowed(stmt) { t.Fatal("utility is not SQL PREPARE body, planned query, row result or aborted-transaction control") }
	for _, state := range []TxState{TxIdle, TxActive, TxAborted} {
		if tag, counted, insert, ok := ClassifyCommandTag(stmt, state); tag != "SECURITY LABEL" || counted || insert || !ok { t.Fatal(tag, counted, insert, ok) }
	}
}
