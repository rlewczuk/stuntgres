package session

import "stuntgres/internal/parser"

// ForeignGrantKind reports whether a GRANT object kind is a wrapper or server.
func ForeignGrantKind(kind string) bool {
	return kind == "foreign server" || kind == "foreign-data wrapper"
}

// ModifyingTag names the command that a read-only transaction refuses; it
// is empty when stmt does not modify anything.
func ModifyingTag(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.Subscription:
		switch stmt.(*parser.Subscription).Action {
		case "create":
			return "CREATE SUBSCRIPTION"
		case "drop":
			return "DROP SUBSCRIPTION"
		default:
			return "ALTER SUBSCRIPTION"
		}
	case *parser.ForeignDDL:
		return ForeignDDLTag(stmt.(*parser.ForeignDDL))
	case *parser.CreateDatabaseStmt:
		return "CREATE DATABASE"
	case *parser.CreateTablespace:
		return "CREATE TABLESPACE"
	case *parser.Truncate:
		return "TRUNCATE TABLE"
	case *parser.AlterOwner:
		s := stmt.(*parser.AlterOwner)
		switch s.Kind {
		case "database":
			return "ALTER DATABASE"
		case "large object":
			return "ALTER LARGE OBJECT"
		case "type":
			return "ALTER TYPE"
		case "domain":
			return "ALTER DOMAIN"
		case "materialized view":
			return "ALTER MATERIALIZED VIEW"
		default:
			return "ALTER TABLE"
		}
	case *parser.ReassignOwned:
		return "REASSIGN OWNED"
	case *parser.DropOwned:
		return "DROP OWNED"
	case *parser.RefreshMaterializedView:
		return "REFRESH MATERIALIZED VIEW"
	case *parser.CreateEventTrigger:
		return "CREATE EVENT TRIGGER"
	case *parser.AlterEventTrigger:
		return "ALTER EVENT TRIGGER"
	case *parser.DropEventTrigger:
		return "DROP EVENT TRIGGER"
	case *parser.CreateTrigger:
		return "CREATE TRIGGER"
	case *parser.AlterTrigger:
		return "ALTER TRIGGER"
	case *parser.DropTrigger:
		return "DROP TRIGGER"
	case *parser.CreateRule:
		return "CREATE RULE"
	case *parser.AlterRule:
		return "ALTER RULE"
	case *parser.DropRule:
		return "DROP RULE"
	case *parser.CreatePolicy:
		return "CREATE POLICY"
	case *parser.AlterPolicy:
		return "ALTER POLICY"
	case *parser.DropPolicy:
		return "DROP POLICY"
	case *parser.Copy:
		if stmt.(*parser.Copy).From {
			return "COPY FROM"
		}
	case *parser.CreateIndex:
		return "CREATE INDEX"
	case *parser.DropIndex:
		return "DROP INDEX"
	case *parser.AlterIndex:
		return "ALTER INDEX"
	case *parser.Reindex:
		return "REINDEX"
	case *parser.CreateView:
		return "CREATE VIEW"
	case *parser.AlterView:
		if stmt.(*parser.AlterView).Materialized {
			return "ALTER MATERIALIZED VIEW"
		}
		return "ALTER VIEW"
	case *parser.DropView:
		if stmt.(*parser.DropView).Materialized {
			return "DROP MATERIALIZED VIEW"
		}
		return "DROP VIEW"
	case *parser.Select:
		if sel := stmt.(*parser.Select); sel.Locking != nil {
			return "SELECT " + sel.Locking[0].Mode
		}
		if parser.HasModifyingCTE(stmt) {
			return "SELECT"
		}
	case *parser.CreateSequence:
		return "CREATE SEQUENCE"
	case *parser.AlterSequence:
		return "ALTER SEQUENCE"
	case *parser.DropSequence:
		return "DROP SEQUENCE"
	case *parser.Insert:
		return "INSERT"
	case *parser.Merge:
		return "MERGE"
	case *parser.Update:
		return "UPDATE"
	case *parser.Delete:
		return "DELETE"
	case *parser.CreateQueryTable:
		if stmt.(*parser.CreateQueryTable).Form == parser.CreateMaterializedView {
			return "CREATE MATERIALIZED VIEW"
		}
		if stmt.(*parser.CreateQueryTable).Form == parser.SelectInto {
			return "SELECT INTO"
		}
		return "CREATE TABLE AS"
	case *parser.CreateTable:
		return "CREATE TABLE"
	case *parser.DropTable:
		return "DROP TABLE"
	case *parser.CreateRange, *parser.CreateComposite, *parser.CreateEnum:
		return "CREATE TYPE"
	case *parser.AlterEnum, *parser.AlterComposite, *parser.AlterType:
		return "ALTER TYPE"
	case *parser.DropType:
		return "DROP TYPE"
	case *parser.CreateDomain:
		return "CREATE DOMAIN"
	case *parser.DropDomain:
		return "DROP DOMAIN"
	case *parser.AlterDomain:
		return "ALTER DOMAIN"
	case *parser.CreateParser:
		return "CREATE TEXT SEARCH PARSER"
	case *parser.AlterParser:
		return "ALTER TEXT SEARCH PARSER"
	case *parser.DropParser:
		return "DROP TEXT SEARCH PARSER"
	case *parser.CreateConfiguration:
		return "CREATE TEXT SEARCH CONFIGURATION"
	case *parser.AlterConfiguration:
		return "ALTER TEXT SEARCH CONFIGURATION"
	case *parser.DropConfiguration:
		return "DROP TEXT SEARCH CONFIGURATION"
	case *parser.CreateDictionary:
		return "CREATE TEXT SEARCH DICTIONARY"
	case *parser.AlterDictionary:
		return "ALTER TEXT SEARCH DICTIONARY"
	case *parser.DropDictionary:
		return "DROP TEXT SEARCH DICTIONARY"
	case *parser.CreateTemplate:
		return "CREATE TEXT SEARCH TEMPLATE"
	case *parser.AlterTemplate:
		return "ALTER TEXT SEARCH TEMPLATE"
	case *parser.DropTemplate:
		return "DROP TEXT SEARCH TEMPLATE"
	case *parser.CreateCollation:
		return "CREATE COLLATION"
	case *parser.AlterCollation:
		return "ALTER COLLATION"
	case *parser.DropCollation:
		return "DROP COLLATION"
	case *parser.CreateStatistics:
		return "CREATE STATISTICS"
	case *parser.DropStatistics:
		return "DROP STATISTICS"
	case *parser.AlterStatisticsTarget:
		return "ALTER STATISTICS"
	case *parser.CreateConversion:
		return "CREATE CONVERSION"
	case *parser.AlterConversion:
		return "ALTER CONVERSION"
	case *parser.DropConversion:
		return "DROP CONVERSION"
	case *parser.CreateAggregate:
		return "CREATE AGGREGATE"
	case *parser.CreateCast:
		return "CREATE CAST"
	case *parser.DropCast:
		return "DROP CAST"
	case *parser.CreateFunction:
		if stmt.(*parser.CreateFunction).Procedure {
			return "CREATE PROCEDURE"
		}
		return "CREATE FUNCTION"
	case *parser.CreateLanguage:
		return "CREATE LANGUAGE"
	case *parser.AlterRoutine:
		return routineAlterCommandTag(stmt.(*parser.AlterRoutine).Kind)
	case *parser.DropRoutine:
		return routineDropCommandTag(stmt.(*parser.DropRoutine).Kind)
	case *parser.CreateOperator:
		return "CREATE OPERATOR"
	case *parser.AlterOperator:
		return "ALTER OPERATOR"
	case *parser.DropOperator:
		return "DROP OPERATOR"
	case *parser.CreateOperatorFamily:
		return "CREATE OPERATOR FAMILY"
	case *parser.AlterOperatorFamily:
		return "ALTER OPERATOR FAMILY"
	case *parser.DropOperatorFamily:
		return "DROP OPERATOR FAMILY"
	case *parser.CreateAccessMethod:
		return "CREATE ACCESS METHOD"
	case *parser.DropAccessMethod:
		return "DROP ACCESS METHOD"
	case *parser.CreateOperatorClass:
		return "CREATE OPERATOR CLASS"
	case *parser.AlterOperatorClass:
		return "ALTER OPERATOR CLASS"
	case *parser.DropOperatorClass:
		return "DROP OPERATOR CLASS"
	case *parser.AlterTableAllInTablespace, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableRenameConstraint, *parser.AlterTableDefault, *parser.AlterTable:
		return "ALTER TABLE"
	case *parser.CreateExtension:
		return "CREATE EXTENSION"
	case *parser.DropExtension:
		return "DROP EXTENSION"
	case *parser.AlterExtensionUpdate:
		return "ALTER EXTENSION"
	case *parser.CreateSchema:
		return "CREATE SCHEMA"
	case *parser.AlterSchema:
		return "ALTER SCHEMA"
	case *parser.DropSchema:
		return "DROP SCHEMA"
	case *parser.CreateRole:
		return "CREATE ROLE"
	case *parser.AlterDefaultPrivileges:
		return "ALTER DEFAULT PRIVILEGES"
	case *parser.ObjectGrant:
		if stmt.(*parser.ObjectGrant).Revoke {
			return "REVOKE"
		}
		return "GRANT"
	case *parser.RoleMembership:
		m := stmt.(*parser.RoleMembership)
		if m.Alias {
			return "ALTER ROLE"
		}
		if m.Revoke {
			return "REVOKE ROLE"
		}
		return "GRANT ROLE"
	case *parser.AlterRole, *parser.RenameRole, *parser.AlterRoleSettings:
		return "ALTER ROLE"
	case *parser.DropRole:
		return "DROP ROLE"
	case *parser.Comment:
		return "COMMENT"
	}
	return ""
}

// ExplicitUtility reports whether stmt is a utility that the root dispatch
// gates in explicit transactions; the root keeps the exceptions and errors.
func ExplicitUtility(stmt parser.Statement) bool {
	if c, ok := stmt.(*parser.Comment); ok {
		switch c.Kind {
		case parser.CommentTable, parser.CommentColumn, parser.CommentForeignTable, parser.CommentSchema, parser.CommentRole, parser.CommentSequence:
			return false
		}
	}
	switch stmt.(type) {
	case *parser.Subscription:
		return true
	case *parser.AlterSystem:
		return true
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger:
		return true
	case *parser.CreateDatabaseStmt:
		return true
	case *parser.CreateTablespace:
		return true
	case *parser.AlterOwner, *parser.ReassignOwned, *parser.DropOwned:
		return true
	case *parser.RefreshMaterializedView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return true
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger, *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return true
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView:
		return true
	case *parser.CreateStatistics, *parser.DropStatistics, *parser.AlterStatisticsTarget:
		return true
	case *parser.CreateParser, *parser.AlterParser, *parser.DropParser:
		return true
	case *parser.CreateTemplate, *parser.AlterTemplate, *parser.DropTemplate, *parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary, *parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration:
		return true
	case *parser.CreateComposite, *parser.AlterComposite, *parser.AlterType, *parser.DropType, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateDomain, *parser.DropDomain, *parser.AlterDomain, *parser.CreateQueryTable, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return true
	case *parser.CreateCast, *parser.DropCast, *parser.CreateFunction, *parser.CreateLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		return true
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return true
	case *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return true
	case *parser.RenameRole, *parser.AlterRoleSettings:
		return true
	case *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableRenameConstraint, *parser.AlterTableDefault, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization:
		return true
	case *parser.RoleMembership:
		return true
	}
	return false
}

// TransactionalSchemaUtility reports whether stmt runs on the transaction
// journal. It is the bounded relation/schema/type/routine/ownership and
// collation/conversion/cast integration boundary, including ordinary and
// concurrent materialized refresh, not a relaxation of independent standalone
// utility policies. Describe uses the
// same boundary as Execute, but query-derived creation still analyzes its
// source.
func TransactionalSchemaUtility(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.Subscription:
		return true
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger:
		return true
	case *parser.CreateFunction:
		return !s.Procedure
	case *parser.CreateAggregate, *parser.AlterRoutine, *parser.DropRoutine,
		*parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		return true
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule, *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger:
		return true
	case *parser.CreateAccessMethod, *parser.DropAccessMethod,
		*parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily,
		*parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return true
	case *parser.CreateTable, *parser.DropTable, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema,
		*parser.AlterTable, *parser.AlterTableRenameTable, *parser.AlterTableRenameColumn,
		*parser.AlterTableRenameConstraint, *parser.AlterTableSetSchema, *parser.AlterTableDefault,
		*parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition,
		*parser.AlterTableAllInTablespace, *parser.CreateView, *parser.AlterView, *parser.DropView,
		*parser.RefreshMaterializedView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy,
		*parser.CreateDomain, *parser.CreateComposite, *parser.AlterDomain, *parser.AlterComposite,
		*parser.AlterType, *parser.DropDomain, *parser.DropType,
		*parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation,
		*parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary,
		*parser.CreateTemplate, *parser.AlterTemplate, *parser.DropTemplate,
		*parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration,
		*parser.CreateParser, *parser.AlterParser, *parser.DropParser,
		*parser.CreateStatistics, *parser.DropStatistics, *parser.AlterStatisticsTarget,
		*parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion,
		*parser.CreateCast, *parser.DropCast:
		return true
	case *parser.AlterOwner:
		return s.Kind == "table" || s.Kind == "type" || s.Kind == "domain" || s.Kind == "database" || s.Kind == "large object"
	case *parser.ReassignOwned, *parser.DropOwned:
		return true
	case *parser.CreateQueryTable:
		return s.Form == parser.CreateTableAs || s.Form == parser.SelectInto || s.Form == parser.CreateMaterializedView
	}
	return false
}

// EarlyStandaloneUtility identifies syntax-only utilities whose transaction
// refusal must precede relation waits; other admission precedence is unchanged.
func EarlyStandaloneUtility(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return true
	case *parser.AlterTableDetachPartition:
		return s.Mode == parser.DetachConcurrent
	}
	return false
}

// ReadOnlyExempt reports whether a read-only transaction skips the utility
// refusal for stmt; data statements check their own targets.
func ReadOnlyExempt(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Call, *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.FetchCursor, *parser.CloseCursor, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

// ReadOnlyRefusal refuses a modifying utility in a read-only transaction.
func ReadOnlyRefusal(stmt parser.Statement, readOnly bool) error {
	if !readOnly || ReadOnlyExempt(stmt) {
		return nil
	}
	if tag := ModifyingTag(stmt); tag != "" {
		return sqlError("25006", "cannot execute "+tag+" in a read-only transaction")
	}
	return nil
}

// ExplicitUtilityRefusal refuses an ExplicitUtility that the root did not
// exempt: block-only utilities inside a block, and any other in an explicit
// transaction. inBlock is TransactionOwner.InBlock.
func ExplicitUtilityRefusal(stmt parser.Statement, state TxState, inBlock bool) error {
	command := ""
	switch s := stmt.(type) {
	case *parser.AlterSystem:
		command = "ALTER SYSTEM"
	case *parser.CreateDatabaseStmt:
		command = "CREATE DATABASE"
	case *parser.CreateTablespace:
		command = "CREATE TABLESPACE"
	case *parser.CreateIndex:
		if s.Concurrent.Character != 0 {
			command = "CREATE INDEX CONCURRENTLY"
		}
	case *parser.DropIndex:
		if s.Concurrent.Character != 0 {
			command = "DROP INDEX CONCURRENTLY"
		}
	}
	if command != "" && inBlock {
		return sqlError("25001", command+" cannot run inside a transaction block")
	}
	if command == "CREATE DATABASE" || command == "CREATE TABLESPACE" || command == "ALTER SYSTEM" || state != TxActive {
		return nil
	}
	return UnsupportedInBlockError()
}

// UnsupportedInBlockError refuses a utility in an explicit transaction.
func UnsupportedInBlockError() error {
	return sqlError("0A000", "statement is unsupported in an explicit transaction")
}
