package session

import "stuntgres/internal/parser"

// EventCommand classifies syntax before affected-object resolution. This closed
// inventory is deliberately separate from modifying/read-only and completion
// tags. Unknown/new ASTs are never accidentally event-eligible.
func EventCommand(stmt parser.Statement) (tag string, eligible bool) {
	switch s := stmt.(type) {
	case *parser.CreateQueryTable:
		if s.Form == parser.SelectInto {
			return "SELECT INTO", true
		}
		return QueryCreationUtilityTag(s.Form), true
	case *parser.Comment:
		switch s.Kind {
		case parser.CommentTable, parser.CommentColumn, parser.CommentSchema, parser.CommentSequence, parser.CommentForeignTable:
			return "COMMENT", true
		default:
			return "COMMENT", false
		}
	case *parser.ObjectGrant:
		tag = "GRANT"
		if s.Revoke {
			tag = "REVOKE"
		}
		switch s.Kind {
		case "", "sequence", "foreign server", "foreign-data wrapper", "function", "procedure", "routine", "schema", "type", "domain", "language", "large object":
			return tag, true
		default:
			return tag, false
		}
	case *parser.AlterOwner:
		switch s.Kind {
		case "table":
			return "ALTER TABLE", true
		case "type":
			return "ALTER TYPE", true
		case "domain":
			return "ALTER DOMAIN", true
		case "materialized view":
			return "ALTER MATERIALIZED VIEW", true
		default:
			return "", false
		}
	case *parser.ForeignDDL:
		return ForeignDDLTag(s), true
	case *parser.CreateIndex:
		// Incumbent CONCURRENTLY CREATE owns independent publication phases,
		// not this transactional utility's one Finish boundary.
		return "CREATE INDEX", s.Concurrent.Character == 0
	case *parser.CreateFunction:
		if s.Procedure {
			return "CREATE PROCEDURE", false
		}
		return "CREATE FUNCTION", true
	case *parser.CreateTable, *parser.DropTable,
		*parser.AlterTable, *parser.AlterTableDefault, *parser.AlterTableOf,
		*parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition,
		*parser.AlterTableRenameColumn, *parser.AlterTableRenameTable,
		*parser.AlterTableSetSchema, *parser.AlterTableRenameConstraint, *parser.AlterTableAllInTablespace,
		*parser.CreateView, *parser.AlterView, *parser.DropView, *parser.RefreshMaterializedView,
		*parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence,
		*parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum, *parser.CreateComposite, *parser.AlterComposite, *parser.AlterType, *parser.DropType,
		*parser.CreateDomain, *parser.AlterDomain, *parser.DropDomain,
		*parser.CreateSchema, *parser.AlterSchema, *parser.DropSchema,
		*parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation,
		*parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion,
		*parser.CreateCast, *parser.DropCast,
		*parser.CreateAggregate, *parser.AlterRoutine, *parser.DropRoutine,
		*parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator,
		*parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily,
		*parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass,
		*parser.CreateAccessMethod, *parser.DropAccessMethod,
		*parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger,
		*parser.CreateRule, *parser.AlterRule, *parser.DropRule,
		*parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy,
		*parser.CreateExtension, *parser.DropExtension, *parser.AlterExtensionUpdate,
		*parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary,
		*parser.AlterDefaultPrivileges, *parser.DropOwned, *parser.AlterIndex, *parser.DropIndex, *parser.Reindex:
		tag, _, _, _ = ClassifyCommandTag(stmt, TxIdle)
		return tag, true
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger,
		*parser.CreateLanguage, *parser.CreateDatabaseStmt, *parser.CreateTablespace, *parser.CreateRole, *parser.AlterRole, *parser.DropRole,
		*parser.RoleMembership, *parser.ReassignOwned,
		*parser.Call, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge, *parser.Select, *parser.Copy,
		*parser.Do, *parser.Load, *parser.Vacuum, *parser.Truncate, *parser.LockTable,
		*parser.PrepareSQL, *parser.ExecuteSQL, *parser.DeallocateSQL, *parser.DeclareCursor, *parser.FetchCursor, *parser.CloseCursor,
		*parser.Listen, *parser.Unlisten, *parser.Notify, *parser.Discard,
		*parser.Set, *parser.Reset, *parser.Show, *parser.SettingCall, *parser.RoleSelection, *parser.SessionAuthorization,
		*parser.TransactionControl, *parser.TransactionCharacteristics, *parser.SavepointControl, *parser.SetConstraints:
		return "", false
	default:
		return "", false
	}
}
