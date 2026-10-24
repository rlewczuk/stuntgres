package engine

import "stuntgres/internal/parser"

// transactionalSchemaUtility is the bounded relation/schema integration boundary,
// not a relaxation of independent standalone utility policies. Describe uses the
// same boundary as Execute, but query-derived creation still analyzes its source.
func transactionalSchemaUtility(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.AlterTrigger, *parser.DropTrigger:
		return true
	case *parser.CreateTable, *parser.DropTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema,
		*parser.AlterTable, *parser.AlterTableRenameTable, *parser.AlterTableRenameColumn,
		*parser.AlterTableRenameConstraint, *parser.AlterTableSetSchema, *parser.AlterTableDefault,
		*parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition,
		*parser.AlterTableAllInTablespace, *parser.CreateView, *parser.AlterView, *parser.DropView:
		return true
	case *parser.AlterOwner:
		return s.Kind == "table"
	case *parser.CreateQueryTable:
		return s.Form == parser.CreateTableAs || s.Form == parser.SelectInto || s.Form == parser.CreateMaterializedView
	}
	return false
}
