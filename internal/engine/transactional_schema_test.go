package engine

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
	} {
		if !transactionalSchemaUtility(stmt) {
			t.Errorf("missing admission: %T", stmt)
		}
	}
	for _, stmt := range []parser.Statement{
		&parser.RefreshMaterializedView{},
		&parser.AlterOwner{Kind: "view"}, &parser.AlterOwner{Kind: "type"}, &parser.AlterOwner{Kind: "domain"}, &parser.AlterOwner{Kind: "database"},
		&parser.CreateDatabaseStmt{}, &parser.CreateTablespace{}, &parser.CreateIndex{}, &parser.DropIndex{},
		&parser.CreateComposite{}, &parser.CreateDomain{}, &parser.CreateFunction{}, &parser.CreateRole{}, &parser.Comment{},
		&parser.ObjectGrant{}, &parser.ReassignOwned{}, &parser.DropOwned{}, &parser.CreateConversion{},
	} {
		if transactionalSchemaUtility(stmt) {
			t.Errorf("widened admission: %T", stmt)
		}
	}
}
