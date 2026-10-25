package session

import (
	"stuntgres/internal/parser"
	"testing"
)

func TestAggregateLifecycleCommandTag(t *testing.T) {
	for _, tc := range []struct {
		stmt parser.Statement
		tag  string
	}{
		{&parser.AlterRoutine{Kind: "aggregate"}, "ALTER AGGREGATE"},
		{&parser.DropRoutine{Kind: "aggregate"}, "DROP AGGREGATE"},
	} {
		if !TransactionalSchemaUtility(tc.stmt) || !ExplicitUtility(tc.stmt) || DescribeBeforeSnapshot(tc.stmt) != DescribeOperation || DescribeAfterSnapshot(tc.stmt) {
			t.Fatalf("lost utility classification %T", tc.stmt)
		}
		if got := sqlText(ReadOnlyRefusal(tc.stmt, true)); got != "25006 cannot execute "+tc.tag+" in a read-only transaction" {
			t.Fatal(got)
		}
		if err := ReadOnlyRefusal(tc.stmt, false); err != nil {
			t.Fatal(err)
		}
	}
}
