package session

import (
	"stuntgres/internal/parser"
	"testing"
)

func TestTransactionalSchemaAdmission(t *testing.T) {
	for _, stmt := range []parser.Statement{
		&parser.CreateTable{}, &parser.DropTable{}, &parser.CreateSchema{}, &parser.AlterSchema{}, &parser.DropSchema{},
		&parser.AlterTable{}, &parser.AlterTableRenameTable{}, &parser.AlterTableRenameColumn{}, &parser.AlterTableRenameConstraint{},
		&parser.AlterTableSetSchema{}, &parser.AlterTableDefault{}, &parser.AlterTableOf{}, &parser.AlterTableAttachPartition{},
		&parser.AlterTableDetachPartition{}, &parser.AlterTableAllInTablespace{}, &parser.AlterOwner{Kind: "table"},
		&parser.CreateQueryTable{Form: parser.CreateTableAs}, &parser.CreateQueryTable{Form: parser.SelectInto},
		&parser.CreateView{}, &parser.AlterView{}, &parser.AlterView{Materialized: true},
		&parser.DropView{}, &parser.DropView{Materialized: true},
		&parser.CreateQueryTable{Form: parser.CreateMaterializedView},
		&parser.RefreshMaterializedView{}, parsedStatement(t, "REFRESH MATERIALIZED VIEW CONCURRENTLY admission_mv"),
		&parser.CreateComposite{}, &parser.CreateDomain{}, &parser.AlterDomain{}, &parser.AlterComposite{},
		&parser.AlterType{}, &parser.DropDomain{}, &parser.DropType{},
		&parser.CreateTrigger{}, parsedStatement(t, "CREATE OR REPLACE TRIGGER admission BEFORE INSERT ON admission_t FOR EACH ROW EXECUTE FUNCTION admission_fn()"),
		&parser.AlterTrigger{Action: "rename"}, &parser.AlterTrigger{Action: "depends"}, &parser.AlterTrigger{Action: "no_depends"}, &parser.DropTrigger{},
		&parser.AlterOwner{Kind: "type"}, &parser.AlterOwner{Kind: "domain"}, &parser.AlterOwner{Kind: "database"},
		&parser.AlterView{Action: "owner"}, &parser.AlterView{Action: "owner", Materialized: true},
		&parser.ReassignOwned{}, &parser.DropOwned{},
		&parser.CreateFunction{}, &parser.CreateAggregate{}, &parser.AlterRoutine{}, &parser.DropRoutine{},
		&parser.CreateOperator{}, &parser.AlterOperator{}, &parser.DropOperator{},
	} {
		if !TransactionalSchemaUtility(stmt) {
			t.Errorf("missing admission: %T", stmt)
		}
	}
	for _, stmt := range []parser.Statement{
		&parser.AlterOwner{Kind: "view"}, &parser.AlterOwner{Kind: "unknown"}, &parser.AlterOwner{},
		&parser.CreateDatabaseStmt{}, &parser.CreateTablespace{}, &parser.CreateIndex{}, &parser.DropIndex{},
		&parser.CreateFunction{Procedure: true}, &parser.CreateLanguage{}, &parser.CreateCast{}, &parser.DropCast{}, &parser.CreateRole{}, &parser.Comment{},
		&parser.ObjectGrant{}, &parser.CreateConversion{},
	} {
		if TransactionalSchemaUtility(stmt) {
			t.Errorf("widened admission: %T", stmt)
		}
	}
}

func parsedStatement(t *testing.T, sql string) parser.Statement {
	t.Helper()
	s, err := parser.Parse(sql)
	if err != nil || len(s) != 1 {
		t.Fatal(len(s), err)
	}
	return s[0]
}
