package session

import (
	"strings"

	"stuntgres/internal/parser"
)

// ForeignDDLTag is the command tag of a foreign-data DDL statement.
func ForeignDDLTag(s *parser.ForeignDDL) string {
	if s.Action == "import" {
		return "IMPORT FOREIGN SCHEMA"
	}
	kind := "FOREIGN TABLE"
	switch s.Kind {
	case "wrapper":
		kind = "FOREIGN DATA WRAPPER"
	case "server":
		kind = "SERVER"
	case "mapping":
		kind = "USER MAPPING"
	}
	return strings.ToUpper(s.Action) + " " + kind
}

// ClassifyCommandTag describes only admitted AST kinds, never SQL source text.
// Count formatting belongs to Execute after the actual statement has succeeded.
func ClassifyCommandTag(stmt parser.Statement, before TxState) (base string, counted bool, insert bool, ok bool) {
	switch s := stmt.(type) {
	case *parser.Insert:
		return "INSERT", true, true, true
	case *parser.Update:
		return "UPDATE", true, false, true
	case *parser.Delete:
		return "DELETE", true, false, true
	case *parser.Merge:
		return "MERGE", true, false, true
	case *parser.Select, *parser.SettingCall:
		return "SELECT", true, false, true
	case *parser.Show:
		base = "SHOW"
	case *parser.Copy:
		// Protocol stream execution shares this count; embedded Execute retains CopyBoundary.
		return "COPY", true, false, true
	case *parser.CreateQueryTable:
		switch s.Form {
		case parser.CreateTableAs, parser.SelectInto:
			if s.NoData {
				base = QueryCreationUtilityTag(s.Form)
			} else {
				return "SELECT", true, false, true
			}
		case parser.CreateMaterializedView:
			if s.NoData {
				base = QueryCreationUtilityTag(s.Form)
			} else {
				return "SELECT", true, false, true
			}
		default:
			return "", false, false, false
		}
	case *parser.ForeignDDL:
		base = ForeignDDLTag(s)
	case *parser.CreateDatabaseStmt:
		base = "CREATE DATABASE"
	case *parser.CreateTablespace:
		base = "CREATE TABLESPACE"
	case *parser.Load:
		base = "LOAD"
	case *parser.CreateTable:
		base = "CREATE TABLE"
	case *parser.DropTable:
		base = "DROP TABLE"
	case *parser.AlterTableAllInTablespace, *parser.AlterTable, *parser.AlterTableDefault, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition,
		*parser.AlterTableRenameColumn, *parser.AlterTableRenameTable,
		*parser.AlterTableSetSchema, *parser.AlterTableRenameConstraint:
		base = "ALTER TABLE"
	case *parser.CreateRange, *parser.CreateEnum, *parser.CreateComposite:
		base = "CREATE TYPE"
	case *parser.AlterEnum, *parser.AlterComposite, *parser.AlterType:
		base = "ALTER TYPE"
	case *parser.DropType:
		base = "DROP TYPE"
	case *parser.CreateDomain:
		base = "CREATE DOMAIN"
	case *parser.DropDomain:
		base = "DROP DOMAIN"
	case *parser.AlterDomain:
		base = "ALTER DOMAIN"
	case *parser.CreateIndex:
		base = "CREATE INDEX"
	case *parser.AlterIndex:
		base = "ALTER INDEX"
	case *parser.DropIndex:
		base = "DROP INDEX"
	case *parser.Reindex:
		base = "REINDEX"
	case *parser.Vacuum:
		base = "VACUUM"
	case *parser.CreateView:
		base = "CREATE VIEW"
	case *parser.AlterView:
		if s.Materialized {
			base = "ALTER MATERIALIZED VIEW"
		} else {
			base = "ALTER VIEW"
		}
	case *parser.DropView:
		if s.Materialized {
			base = "DROP MATERIALIZED VIEW"
		} else {
			base = "DROP VIEW"
		}
	case *parser.RefreshMaterializedView:
		base = "REFRESH MATERIALIZED VIEW"
	case *parser.CreateSequence:
		base = "CREATE SEQUENCE"
	case *parser.AlterSequence:
		base = "ALTER SEQUENCE"
	case *parser.DropSequence:
		base = "DROP SEQUENCE"
	case *parser.CreateSchema:
		base = "CREATE SCHEMA"
	case *parser.AlterSchema:
		base = "ALTER SCHEMA"
	case *parser.DropSchema:
		base = "DROP SCHEMA"
	case *parser.CreateCollation:
		base = "CREATE COLLATION"
	case *parser.AlterCollation:
		base = "ALTER COLLATION"
	case *parser.DropCollation:
		base = "DROP COLLATION"
	case *parser.CreateConversion:
		base = "CREATE CONVERSION"
	case *parser.AlterConversion:
		base = "ALTER CONVERSION"
	case *parser.DropConversion:
		base = "DROP CONVERSION"
	case *parser.CreateAggregate:
		base = "CREATE AGGREGATE"
	case *parser.CreateCast:
		base = "CREATE CAST"
	case *parser.DropCast:
		base = "DROP CAST"
	case *parser.CreateFunction:
		base = "CREATE FUNCTION"
		if s.Procedure {
			base = "CREATE PROCEDURE"
		}
	case *parser.CreateLanguage:
		base = "CREATE LANGUAGE"
	case *parser.Do:
		base = "DO"
	case *parser.AlterRoutine:
		base = routineAlterCommandTag(s.Kind)
	case *parser.DropRoutine:
		base = routineDropCommandTag(s.Kind)
	case *parser.CreateOperator:
		base = "CREATE OPERATOR"
	case *parser.AlterOperator:
		base = "ALTER OPERATOR"
	case *parser.DropOperator:
		base = "DROP OPERATOR"
	case *parser.CreateOperatorFamily:
		base = "CREATE OPERATOR FAMILY"
	case *parser.AlterOperatorFamily:
		base = "ALTER OPERATOR FAMILY"
	case *parser.DropOperatorFamily:
		base = "DROP OPERATOR FAMILY"
	case *parser.CreateAccessMethod:
		base = "CREATE ACCESS METHOD"
	case *parser.DropAccessMethod:
		base = "DROP ACCESS METHOD"
	case *parser.CreateOperatorClass:
		base = "CREATE OPERATOR CLASS"
	case *parser.AlterOperatorClass:
		base = "ALTER OPERATOR CLASS"
	case *parser.DropOperatorClass:
		base = "DROP OPERATOR CLASS"
	case *parser.CreateRole:
		base = "CREATE ROLE"
	case *parser.AlterRole:
		base = "ALTER ROLE"
	case *parser.DropRole:
		base = "DROP ROLE"
	case *parser.CreateTrigger:
		base = "CREATE TRIGGER"
	case *parser.CreateRule:
		base = "CREATE RULE"
	case *parser.AlterRule:
		base = "ALTER RULE"
	case *parser.DropRule:
		base = "DROP RULE"
	case *parser.CreatePolicy:
		base = "CREATE POLICY"
	case *parser.AlterPolicy:
		base = "ALTER POLICY"
	case *parser.DropPolicy:
		base = "DROP POLICY"
	case *parser.CreateExtension:
		base = "CREATE EXTENSION"
	case *parser.DropExtension:
		base = "DROP EXTENSION"
	case *parser.AlterExtensionUpdate:
		base = "ALTER EXTENSION"
	case *parser.AlterOwner:
		// Keep the ownership family closed rather than uppercasing arbitrary AST text.
		switch s.Kind {
		case "table":
			base = "ALTER TABLE"
		case "type":
			base = "ALTER TYPE"
		case "domain":
			base = "ALTER DOMAIN"
		case "database":
			base = "ALTER DATABASE"
		default:
			return "", false, false, false
		}
	case *parser.ReassignOwned:
		base = "REASSIGN OWNED"
	case *parser.DropOwned:
		base = "DROP OWNED"
	case *parser.AlterDefaultPrivileges:
		base = "ALTER DEFAULT PRIVILEGES"
	case *parser.ObjectGrant:
		if s.Revoke {
			base = "REVOKE"
		} else {
			base = "GRANT"
		}
	case *parser.RoleMembership:
		if s.Revoke {
			base = "REVOKE"
		} else {
			base = "GRANT"
		}
	case *parser.Set, *parser.RoleSelection, *parser.SessionAuthorization,
		*parser.TransactionCharacteristics, *parser.SetConstraints:
		base = "SET"
	case *parser.Reset:
		base = "RESET"
	case *parser.TransactionControl:
		switch s.Kind {
		case parser.KindStart:
			base = "BEGIN"
		case parser.KindCommit:
			if before == TxAborted {
				base = "ROLLBACK"
			} else {
				base = "COMMIT"
			}
		case parser.KindRollback:
			base = "ROLLBACK"
		case parser.KindPrepareTransaction:
			base = "PREPARE TRANSACTION"
		case parser.KindCommitPrepared:
			base = "COMMIT PREPARED"
		case parser.KindRollbackPrepared:
			base = "ROLLBACK PREPARED"
		default:
			return "", false, false, false
		}
	case *parser.SavepointControl:
		switch s.Kind {
		case parser.CreateSavepoint:
			base = "SAVEPOINT"
		case parser.RollbackSavepoint:
			base = "ROLLBACK"
		case parser.ReleaseSavepoint:
			base = "RELEASE"
		default:
			return "", false, false, false
		}
	case *parser.PrepareSQL:
		base = "PREPARE"
	case *parser.ExecuteSQL:
		// Execute rewrites a resolved SQL EXECUTE to its actual body before classification.
		return "", false, false, false
	case *parser.DeallocateSQL:
		base = "DEALLOCATE"
	case *parser.DeclareCursor:
		base = "DECLARE CURSOR"
	case *parser.FetchCursor:
		if s.Move {
			return "MOVE", true, false, true
		}
		return "FETCH", true, false, true
	case *parser.CloseCursor:
		if s.All {
			base = "CLOSE CURSOR ALL"
		} else {
			base = "CLOSE CURSOR"
		}
	case *parser.Listen:
		base = "LISTEN"
	case *parser.Unlisten:
		base = "UNLISTEN"
	case *parser.Notify:
		base = "NOTIFY"
	case *parser.Discard:
		switch s.Category {
		case parser.DiscardAll:
			base = "DISCARD ALL"
		case parser.DiscardPlans:
			base = "DISCARD PLANS"
		case parser.DiscardSequences:
			base = "DISCARD SEQUENCES"
		case parser.DiscardTemp:
			base = "DISCARD TEMP"
		default:
			return "", false, false, false
		}
	case *parser.Truncate:
		base = "TRUNCATE TABLE"
	case *parser.LockTable:
		base = "LOCK TABLE"
	case *parser.Comment:
		base = "COMMENT"
	default:
		return "", false, false, false
	}
	return base, false, false, true
}

// A skipped destination or WITH NO DATA completes as a utility, not a SELECT
// that actually populated zero rows.
func QueryCreationUtilityTag(form parser.QueryTableForm) string {
	if form == parser.CreateMaterializedView {
		return "CREATE MATERIALIZED VIEW"
	}
	return "CREATE TABLE AS"
}

func routineDropCommandTag(kind string) string {
	switch kind {
	case "function":
		return "DROP FUNCTION"
	case "procedure":
		return "DROP PROCEDURE"
	default:
		return "DROP ROUTINE"
	}
}

func routineAlterCommandTag(kind string) string {
	switch kind {
	case "procedure":
		return "ALTER PROCEDURE"
	case "routine":
		return "ALTER ROUTINE"
	default:
		return "ALTER FUNCTION"
	}
}
