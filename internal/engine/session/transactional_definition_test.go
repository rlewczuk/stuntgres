package session

import (
	"stuntgres/internal/parser"
	"testing"
)

func TestTransactionalDefinitionAdmission(t *testing.T) {
	for _, tc := range []struct {
		stmt parser.Statement
		tag  string
	}{
		{&parser.CreateAccessMethod{}, "CREATE ACCESS METHOD"},
		{&parser.DropAccessMethod{}, "DROP ACCESS METHOD"},
		{&parser.CreateOperatorFamily{}, "CREATE OPERATOR FAMILY"},
		{&parser.AlterOperatorFamily{}, "ALTER OPERATOR FAMILY"},
		{&parser.DropOperatorFamily{}, "DROP OPERATOR FAMILY"},
		{&parser.CreateOperatorClass{}, "CREATE OPERATOR CLASS"},
		{&parser.AlterOperatorClass{}, "ALTER OPERATOR CLASS"},
		{&parser.DropOperatorClass{}, "DROP OPERATOR CLASS"},
	} {
		t.Run(tc.tag, func(t *testing.T) {
			if !TransactionalSchemaUtility(tc.stmt) {
				t.Error("definition missing from transactional admission")
			}
			if !ExplicitUtility(tc.stmt) {
				t.Error("lost utility classification")
			}
			if got := DescribeBeforeSnapshot(tc.stmt); got != DescribeOperation {
				t.Errorf("Describe class %v, want quiet operation", got)
			}
			if DescribeAfterSnapshot(tc.stmt) {
				t.Error("utility requires effectful post-snapshot analysis")
			}
			if got := sqlText(ReadOnlyRefusal(tc.stmt, true)); got != "25006 cannot execute "+tc.tag+" in a read-only transaction" {
				t.Error(got)
			}
			if err := ReadOnlyRefusal(tc.stmt, false); err != nil {
				t.Error(err)
			}
		})
	}
	for _, stmt := range []parser.Statement{&parser.CreateRole{}, &parser.CreateFunction{Procedure: true}, &parser.CreateDatabaseStmt{}, &parser.CreateTablespace{}, &parser.CreateIndex{}, &parser.DropIndex{}} {
		if TransactionalSchemaUtility(stmt) {
			t.Errorf("widened unrelated admission: %T", stmt)
		}
	}
}
