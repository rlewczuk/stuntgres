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
	case *parser.CreateComposite, *parser.CreateEnum:
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
	case *parser.CreateCollation:
		return "CREATE COLLATION"
	case *parser.AlterCollation:
		return "ALTER COLLATION"
	case *parser.DropCollation:
		return "DROP COLLATION"
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
	case *parser.AlterRole:
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
	if c, ok := stmt.(*parser.Comment); ok && c.Kind == parser.CommentSequence {
		return false
	}
	if grant, ok := stmt.(*parser.ObjectGrant); ok && grant.Kind == "database" {
		return false
	}
	if grant, ok := stmt.(*parser.ObjectGrant); ok && ForeignGrantKind(grant.Kind) {
		return false
	}
	switch stmt.(type) {
	case *parser.CreateDatabaseStmt:
		return true
	case *parser.CreateTablespace:
		return true
	case *parser.AlterOwner, *parser.ReassignOwned, *parser.DropOwned:
		return true
	case *parser.RefreshMaterializedView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return true
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return true
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex, *parser.CreateView, *parser.AlterView, *parser.DropView:
		return true
	case *parser.CreateComposite, *parser.AlterComposite, *parser.AlterType, *parser.DropType, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.CreateDomain, *parser.DropDomain, *parser.AlterDomain, *parser.CreateQueryTable, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return true
	case *parser.CreateCast, *parser.DropCast, *parser.CreateFunction, *parser.CreateLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator:
		return true
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return true
	case *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		return true
	case *parser.Comment, *parser.AlterTableSetSchema, *parser.AlterTableRenameColumn, *parser.AlterTableRenameTable, *parser.AlterTableRenameConstraint, *parser.AlterTableDefault, *parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema, *parser.CreateTable, *parser.DropTable, *parser.CreateRole, *parser.AlterRole, *parser.DropRole, *parser.SessionAuthorization:
		return true
	case *parser.RoleMembership, *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		return true
	}
	return false
}

// Only index utilities have a syntax-only Describe and an early Execute gate.
// Other utility admission precedence is deliberately unchanged.
func EarlyIndexUtility(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.CreateIndex, *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return true
	}
	return false
}
