package session

import (
	"stuntgres/internal/parser"
	"testing"
)

func TestDatabaseDropClassification(t *testing.T) {
	statement := &parser.DropDatabaseStmt{}
	if ModifyingTag(statement) != "DROP DATABASE" || !ExplicitUtility(statement) || TransactionalSchemaUtility(statement) || DescribeBeforeSnapshot(statement) != DescribeOperation {
		t.Fatal("DROP lost closed utility classification")
	}
	if tag, enabled := EventCommand(statement); tag != "" || enabled {
		t.Fatal("cluster database DROP entered local event triggers")
	}
	for _, state := range []TxState{TxActive, TxImplicit} {
		err := ExplicitUtilityRefusal(statement, state, true)
		if err == nil || err.Error() != "stuntgres: DROP DATABASE cannot run inside a transaction block" {
			t.Fatal("DROP became transactional", state, err)
		}
	}
	if err := ExplicitUtilityRefusal(statement, TxImplicit, false); err != nil {
		t.Fatal(err)
	}
	if err := ReadOnlyRefusal(statement, true); err == nil || err.Error() != "stuntgres: cannot execute DROP DATABASE in a read-only transaction" {
		t.Fatal("DROP lost read-only classification", err)
	}
	tag, _, _, _ := ClassifyCommandTag(statement, TxIdle)
	if tag != "DROP DATABASE" {
		t.Fatal("DROP command tag", tag)
	}
}
