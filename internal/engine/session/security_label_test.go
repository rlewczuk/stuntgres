package session

import (
	"testing"

	"stuntgres/internal/parser"
)

func TestSecurityLabelClassification(t *testing.T) {
	for _, kind := range []string{"table", "column", "aggregate", "domain", "event trigger", "foreign table", "function", "large object", "materialized view", "language", "procedure", "publication", "routine", "schema", "sequence", "type", "view", "index", "collation", "conversion", "statistics", "text search parser", "text search dictionary", "text search template", "text search configuration", "access method", "extension", "foreign-data wrapper", "server", "database", "role", "tablespace", "subscription"} {
		s := &parser.SecurityLabel{Kind: kind}
		if tag, counted, insert, ok := ClassifyCommandTag(s, TxIdle); tag != "SECURITY LABEL" || counted || insert || !ok { t.Fatal(kind, tag, counted, insert, ok) }
		if ModifyingTag(s) != "SECURITY LABEL" || ExplicitUtility(s) || TransactionalSchemaUtility(s) { t.Fatal(kind, "transaction admission") }
		if got := sqlText(ReadOnlyRefusal(s, true)); got != "25006 cannot execute SECURITY LABEL in a read-only transaction" { t.Fatal(kind, got) }
		if ReadOnlyRefusal(s, false) != nil || ReadOnlyExempt(s) || EarlyStandaloneUtility(s) { t.Fatal(kind, "ordinary utility gates") }
		if DescribeBeforeSnapshot(s) != DescribeOperation || DescribeAfterSnapshot(s) || SnapshotStatement(s) || SnapshotTargetAdmission(s) || ReplacementRead(s) { t.Fatal(kind, "syntax-only description/target admission") }
		if PreparableBody(s) || ProtocolPlannedStatement(s) || ProtocolHasRows(s, nil) { t.Fatal(kind, "SQL PREPARE/row admission") }
		want := kind != "database" && kind != "role" && kind != "tablespace" && kind != "event trigger"
		if tag, eligible := EventCommand(s); tag != "SECURITY LABEL" || eligible != want { t.Fatal(kind, tag, eligible) }
	}
}
