package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/indexcmds"
	"stuntgres/internal/engine/partitioncmds"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
)

// familyStatements is the composition's Statements: the family cases of both
// dispatch switches, in their historical order.
type familyStatements struct{}

var _ core.Statements = familyStatements{}

func (familyStatements) Execute(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, bool, error) {
	op := scope.Op
	switch stmt.(type) {
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger:
		return dispatched(viewcmds.ExecuteTrigger(e, scope, stmt, count))
	case *parser.CreateFunction, *parser.CreateAggregate:
		return dispatched(schemacmds.ExecuteSQLRoutineDDL(e, scope, stmt, count))
	case *parser.CreateEventTrigger, *parser.AlterEventTrigger, *parser.DropEventTrigger, *parser.CreateDictionary, *parser.AlterDictionary, *parser.DropDictionary,
		*parser.CreateConfiguration, *parser.AlterConfiguration, *parser.DropConfiguration:
		result, err := schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.ForeignDDL:
		result, err := schemacmds.ExecuteForeignDDL(e, scope, stmt.(*parser.ForeignDDL), count)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateDatabaseStmt:
		return dispatched(rolecmds.ExecuteCreateDatabase(e, scope, stmt.(*parser.CreateDatabaseStmt), count))
	case *parser.CreateTablespace:
		return dispatched(schemacmds.ExecuteCreateTablespace(e, op, stmt.(*parser.CreateTablespace), count))
	case *parser.Load:
		return dispatched(schemacmds.ExecuteLoad(e, op, stmt.(*parser.Load), count))
	case *parser.Vacuum:
		return dispatched(tablecmds.ExecuteVacuum(e, scope, stmt.(*parser.Vacuum), count))
	case *parser.Analyze:
		return dispatched(tablecmds.ExecuteAnalyze(e, scope, stmt.(*parser.Analyze), count))
	case *parser.CreateExtension:
		return dispatched(schemacmds.ExecuteExtension(e, scope, stmt.(*parser.CreateExtension), count))
	case *parser.DropExtension:
		return dispatched(schemacmds.ExecuteDropExtension(e, scope, stmt.(*parser.DropExtension), count))
	case *parser.AlterExtensionUpdate:
		return dispatched(schemacmds.ExecuteExtensionUpdate(e, scope, stmt.(*parser.AlterExtensionUpdate), count))
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return dispatched(viewcmds.ExecuteRule(e, scope, stmt, count))
	case *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return dispatched(viewcmds.ExecutePolicy(e, scope, stmt, count))
	case *parser.CreateIndex:
		return dispatched(indexcmds.ExecuteCreateIndex(e, scope, stmt.(*parser.CreateIndex), count))
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return dispatched(indexcmds.ExecuteIndexMaintenance(e, scope, stmt, count))
	case *parser.CreateView:
		return dispatched(viewcmds.ExecuteCreateView(e, scope, stmt.(*parser.CreateView), count))
	case *parser.AlterOwner:
		return dispatched(rolecmds.ExecuteOwnership(e, scope, stmt.(*parser.AlterOwner), count))
	case *parser.ReassignOwned, *parser.DropOwned:
		return dispatched(rolecmds.ExecuteOwned(e, scope, stmt, count))
	case *parser.AlterView:
		if owner := stmt.(*parser.AlterView); owner.Action == "owner" {
			kind := "view"
			if owner.Materialized {
				kind = "materialized view"
			}
			return dispatched(core.ExecuteRelationOwnership(e, scope, owner.Name, owner.Target.Text, owner.OwnerIdentity, kind, owner.IfExists, count))
		}
		return dispatched(viewcmds.ExecuteAlterView(e, scope, stmt.(*parser.AlterView), count))
	case *parser.DropView:
		return dispatched(viewcmds.ExecuteDropView(e, scope, stmt.(*parser.DropView), count))
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return dispatched(tablecmds.ExecuteSequenceDDL(e, scope, stmt, count))
	case *parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum:
		return dispatched(typecmds.ExecuteEnumDDL(e, scope, stmt, count))
	case *parser.AlterDomain:
		domain := stmt.(*parser.AlterDomain)
		var result core.Result
		var err error
		if domain.Action == parser.DomainRename || domain.Action == parser.DomainSetSchema {
			result, err = schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		} else {
			result, err = typecmds.ExecuteAlterDomain(e, scope, domain, count)
		}
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.AlterComposite:
		return dispatched(typecmds.ExecuteAlterComposite(e, scope, op, stmt.(*parser.AlterComposite), count))
	case *parser.Comment:
		if c := stmt.(*parser.Comment); c.Kind == parser.CommentSequence {
			return dispatched(tablecmds.ExecuteSequenceComment(e, scope, c, count))
		}
		result, err := schemacmds.ExecuteComment(e, scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateStatistics, *parser.DropStatistics:
		result, err := schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateSchema, *parser.CreateLanguage, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.AlterType, *parser.DropType, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		result, err := schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		result, err := schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateCast, *parser.DropCast, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		result, err := schemacmds.ExecuteSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.CreateRole, *parser.AlterRole:
		result, err := rolecmds.ExecuteRoleAttributes(e, scope, stmt, count)
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	case *parser.ObjectGrant:
		grant := stmt.(*parser.ObjectGrant)
		if session.ForeignGrantKind(grant.Kind) {
			return dispatched(schemacmds.ExecuteForeignGrant(e, scope, grant, count))
		}
		if grant.Kind == "database" {
			return dispatched(rolecmds.ExecuteDatabaseGrant(e, scope, grant, count))
		}
		return dispatched(rolecmds.ExecuteObjectGrant(e, scope, grant, count))
	case *parser.AlterDefaultPrivileges:
		return dispatched(rolecmds.ExecuteDefaultPrivileges(e, scope, stmt.(*parser.AlterDefaultPrivileges), count))
	case *parser.DropRole, *parser.SessionAuthorization, *parser.RoleSelection, *parser.RoleMembership:
		if drop, ok := stmt.(*parser.DropRole); ok && count == 0 {
			if err := rolecmds.AcquireRoleRetirementLocks(scope, drop); err != nil {
				return core.Result{}, true, err
			}
		}
		result, err := rolecmds.ExecuteRole(e, op, stmt, count, !scope.Owner.InBlock())
		if err == nil {
			scope.CompleteUtility()
		}
		return result, true, err
	}
	return core.Result{}, false, nil
}

func (familyStatements) ExecuteLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stmt parser.Statement, count int) (core.Result, bool, error) {
	op := scope.Op
	switch s := stmt.(type) {
	case *parser.AlterTable:
		return dispatched(tablecmds.AlterTableLocked(e, scope, op, auth, s, count, nil))
	case *parser.AlterTableDefault:
		return dispatched(tablecmds.AlterDefaultLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableAllInTablespace:
		return dispatched(schemacmds.AlterAllTablespaceLocked(e, scope, auth, s, count))
	case *parser.RefreshMaterializedView:
		return dispatched(viewcmds.RefreshMaterializedLocked(e, scope, auth, s, count))
	case *parser.AlterTableOf:
		return dispatched(typecmds.AlterTableOfLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableAttachPartition:
		return dispatched(partitioncmds.AttachPartitionLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableDetachPartition:
		return dispatched(partitioncmds.DetachPartitionLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableRenameConstraint:
		return dispatched(tablecmds.RenameConstraintLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableSetSchema:
		return dispatched(tablecmds.AlterSchemaLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableRenameColumn:
		return dispatched(tablecmds.RenameRelationLocked(e, scope, op, auth, s.Name, s.Column.Text, s.NewName.Text, s.IfExists, true, false, s.Only, count))
	case *parser.AlterTableRenameTable:
		return dispatched(tablecmds.RenameRelationLocked(e, scope, op, auth, s.Name, "", s.NewName.Text, s.IfExists, false, false, false, count))
	case *parser.DropTable:
		return dispatched(tablecmds.DropTableLocked(e, scope, op, auth, s, count))
	case *parser.CreateTable:
		return dispatched(tablecmds.CreateTableLocked(e, scope, op, auth, s, count))
	}
	return core.Result{}, false, nil
}

// dispatched reports a statement as handled with its handler's outcome.
func dispatched(result core.Result, err error) (core.Result, bool, error) {
	return result, true, err
}
