package session

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TestSecurityLabelClassification(t *testing.T) {
	for _, kind := range []string{"table", "column", "aggregate", "domain", "event trigger", "foreign table", "function", "large object", "materialized view", "language", "procedure", "publication", "routine", "schema", "sequence", "type", "view", "index", "collation", "conversion", "statistics", "text search parser", "text search dictionary", "text search template", "text search configuration", "access method", "extension", "foreign-data wrapper", "server", "database", "role", "tablespace", "subscription"} {
		s := &parser.SecurityLabel{Kind: kind}
		if tag, counted, insert, ok := ClassifyCommandTag(s, TxIdle); tag != "SECURITY LABEL" || counted || insert || !ok {
			t.Fatal(kind, tag, counted, insert, ok)
		}
		if ModifyingTag(s) != "SECURITY LABEL" || ExplicitUtility(s) || TransactionalSchemaUtility(s) {
			t.Fatal(kind, "transaction admission")
		}
		if got := sqlText(ReadOnlyRefusal(s, true)); got != "25006 cannot execute SECURITY LABEL in a read-only transaction" {
			t.Fatal(kind, got)
		}
		if ReadOnlyRefusal(s, false) != nil || ReadOnlyExempt(s) || EarlyStandaloneUtility(s) {
			t.Fatal(kind, "ordinary utility gates")
		}
		if DescribeBeforeSnapshot(s) != DescribeOperation || DescribeAfterSnapshot(s) || SnapshotStatement(s) || SnapshotTargetAdmission(s) || ReplacementRead(s) {
			t.Fatal(kind, "syntax-only description/target admission")
		}
		if PreparableBody(s) || ProtocolPlannedStatement(s) || ProtocolHasRows(s, nil) {
			t.Fatal(kind, "SQL PREPARE/row admission")
		}
		want := kind != "database" && kind != "role" && kind != "tablespace" && kind != "event trigger"
		if tag, eligible := EventCommand(s); tag != "SECURITY LABEL" || eligible != want {
			t.Fatal(kind, tag, eligible)
		}
	}
}

func TestJournalSecurityLabelFirstPredecessor(t *testing.T) {
	base := catalog.NewState(1)
	key := catalog.SecurityLabelKey{Object: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}, Provider: "selinux"}
	original := &catalog.SecurityLabelRecord{Label: "P", PolicyID: "sequence-create", PolicyVersion: "1"}
	next := &catalog.SecurityLabelRecord{Label: "Q", PolicyID: "sequence-create", PolicyVersion: "1"}
	base.SetSecurityLabel(key, original)
	journal := NewJournal()
	journal.Cached, journal.CachedBase = catalog.NewRead(nil, base), base
	journal.RecordSecurityLabel(base, key, next)
	if journal.LabelOriginals[key] != original || journal.SecurityLabels[key] != next ||
		journal.CachedBase != nil || journal.Cached.State() != nil || base.SecurityLabel(key) != original {
		t.Fatal("direct label recording lost immutable first predecessor/cache invalidation")
	}
	later := catalog.CloneState(base)
	later.SetSecurityLabel(key, next)
	candidate := catalog.NewCandidate(catalog.NewRead(&catalog.Owner{}, later))
	candidate.RecordSecurityLabel(key, nil)
	journal.RecordCandidateSecurityLabels(later, &candidate)
	if got, present := journal.SecurityLabels[key]; !present || got != nil || journal.LabelOriginals[key] != original {
		t.Fatal("candidate recorder replaced first committed predecessor")
	}
	fresh := key
	fresh.Object.ObjectID++
	journal.RecordSecurityLabel(base, fresh, next)
	if got, present := journal.LabelOriginals[fresh]; !present || got != nil {
		t.Fatal("new sequence lost committed absent association")
	}
	clone := journal.Clone()
	clone.RecordSecurityLabel(later, fresh, original)
	if clone.LabelOriginals[fresh] != nil || clone.SecurityLabels[fresh] != original || journal.SecurityLabels[fresh] != next {
		t.Fatal("clone/savepoint changed parent label maps or nil predecessor")
	}
}
