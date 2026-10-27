package session

import "stuntgres/internal/parser"

// SnapshotStatement reports whether stmt takes a statement snapshot before
// its data phase.
func SnapshotStatement(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Analyze, *parser.Call, *parser.Copy, *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable, *parser.RefreshMaterializedView, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		return true
	}
	return false
}

// SnapshotTargetAdmission reports whether stmt's snapshot also admits its
// write targets: DML and COPY FROM.
func SnapshotTargetAdmission(stmt parser.Statement) bool {
	if copy, ok := stmt.(*parser.Copy); ok {
		return copy.From
	}
	return parser.ModifyingStatement(stmt)
}

// ReplacementRead reports whether stmt, outside a transaction, holds the
// catalog-replacement read fence for its whole run.
func ReplacementRead(stmt parser.Statement) bool {
	switch s := stmt.(type) {
	case *parser.Analyze, *parser.Explain, *parser.Call, *parser.Copy, *parser.Insert, *parser.Update, *parser.Select, *parser.Delete, *parser.Merge, *parser.Discard, *parser.Truncate:
		return true
	case *parser.Comment:
		return s.Kind == parser.CommentTable || s.Kind == parser.CommentForeignTable || s.Kind == parser.CommentColumn
	}
	return false
}

// DescribeClass is how Describe answers a statement without binding it.
type DescribeClass uint8

const (
	// DescribeAnalyzed: the statement goes on to snapshot and analysis.
	DescribeAnalyzed DescribeClass = iota
	// DescribeEmpty: no parameters and no result, without further checks.
	DescribeEmpty
	// DescribeOperation: the utility's operation description, before any
	// snapshot; Execute owns its admission, read-only and block checks.
	DescribeOperation
)

// DescribeBeforeSnapshot classifies stmt before Describe takes a snapshot.
// Copy and the transaction controls are the root's.
func DescribeBeforeSnapshot(stmt parser.Statement) DescribeClass {
	switch s := stmt.(type) {
	case *parser.TransactionCharacteristics, *parser.SetConstraints, *parser.AlterTableRenameConstraint, *parser.Listen, *parser.Unlisten, *parser.Notify:
		return DescribeEmpty
	case *parser.Analyze, *parser.Do, *parser.AlterTableAllInTablespace, *parser.ForeignDDL, *parser.CreateDatabaseStmt, *parser.CreateTablespace, *parser.Load, *parser.Vacuum, *parser.Truncate, *parser.RefreshMaterializedView:
		return DescribeOperation
	case *parser.FetchCursor, *parser.CloseCursor, *parser.PrepareSQL, *parser.ExecuteSQL, *parser.DeallocateSQL, *parser.Discard:
		return DescribeOperation
	case *parser.AlterDomain, *parser.DropDomain, *parser.CreateAccessMethod, *parser.DropAccessMethod:
		return DescribeOperation
	case *parser.CreateStatistics, *parser.DropStatistics:
		return DescribeOperation
	case *parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary, *parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration:
		return DescribeOperation
	case *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation:
		return DescribeOperation
	case *parser.AlterOwner:
		if s.Kind == "domain" {
			return DescribeOperation
		}
	case *parser.CreateCast, *parser.DropCast, *parser.CreateFunction, *parser.CreateLanguage, *parser.CreateAggregate, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		return DescribeOperation
	case *parser.ObjectGrant, *parser.AlterDefaultPrivileges:
		// Analysis is effect-free even inside an explicit transaction.
		return DescribeOperation
	}
	return DescribeAnalyzed
}

// DescribeAfterSnapshot reports whether a definition utility answers with its
// operation description after Describe's analysis snapshot.
func DescribeAfterSnapshot(stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger:
		return true
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger, *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return true
	case *parser.CreateTable, *parser.DropTable, *parser.CreateView, *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy, *parser.AlterTableOf, *parser.AlterTableAttachPartition, *parser.AlterTableDetachPartition, *parser.AlterComposite, *parser.AlterType, *parser.DropType:
		return true
	}
	return false
}
