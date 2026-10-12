package engine

import "stuntgres/internal/parser"

// classifyCommandTag describes only admitted AST kinds, never SQL source text.
// Count formatting belongs to Execute after the actual statement has succeeded.
func classifyCommandTag(stmt parser.Statement, before TxState) (base string, counted bool, insert bool, ok bool) {
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
		// Execution still rejects COPY through the existing CopyBoundary.
		return "COPY", true, false, true
	case *parser.CreateQueryTable:
		switch s.Form {
		case parser.CreateTableAs, parser.SelectInto:
			if s.NoData {
				base = queryCreationUtilityTag(s.Form)
			} else {
				return "SELECT", true, false, true
			}
		case parser.CreateMaterializedView:
			if s.NoData {
				base = queryCreationUtilityTag(s.Form)
			} else {
				return "SELECT", true, false, true
			}
		default:
			return "", false, false, false
		}
	case *parser.CreateTable:
		base = "CREATE TABLE"
	case *parser.DropTable:
		base = "DROP TABLE"
	case *parser.AlterTable, *parser.AlterTableDefault, *parser.AlterTableOf,
		*parser.AlterTableRenameColumn, *parser.AlterTableRenameTable,
		*parser.AlterTableSetSchema, *parser.AlterTableRenameConstraint:
		base = "ALTER TABLE"
	case *parser.CreateEnum, *parser.CreateComposite:
		base = "CREATE TYPE"
	case *parser.AlterEnum, *parser.AlterComposite:
		base = "ALTER TYPE"
	case *parser.DropComposite:
		base = "DROP TYPE"
	case *parser.CreateDomain:
		base = "CREATE DOMAIN"
	case *parser.DropDomain:
		base = "DROP DOMAIN"
	case *parser.CreateIndex:
		base = "CREATE INDEX"
	case *parser.AlterIndex:
		base = "ALTER INDEX"
	case *parser.DropIndex:
		base = "DROP INDEX"
	case *parser.Reindex:
		base = "REINDEX"
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
	case *parser.CreateRole:
		base = "CREATE ROLE"
	case *parser.AlterRole:
		base = "ALTER ROLE"
	case *parser.DropRole:
		base = "DROP ROLE"
	case *parser.CreatePolicy:
		base = "CREATE POLICY"
	case *parser.AlterPolicy:
		base = "ALTER POLICY"
	case *parser.DropPolicy:
		base = "DROP POLICY"
	case *parser.CreateExtension:
		base = "CREATE EXTENSION"
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
func queryCreationUtilityTag(form parser.QueryTableForm) string {
	if form == parser.CreateMaterializedView {
		return "CREATE MATERIALIZED VIEW"
	}
	return "CREATE TABLE AS"
}
