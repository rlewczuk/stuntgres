package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
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
		return dispatched(executeTrigger(e, scope, stmt, count))
	case *parser.CreateFunction, *parser.CreateAggregate:
		return dispatched(executeSQLRoutineDDL(e, scope, stmt, count))
	case *parser.ForeignDDL:
		result, err := executeForeignDDL(e, scope, stmt.(*parser.ForeignDDL), count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateDatabaseStmt:
		return dispatched(executeCreateDatabase(e, scope, stmt.(*parser.CreateDatabaseStmt), count))
	case *parser.CreateTablespace:
		return dispatched(executeCreateTablespace(e, op, stmt.(*parser.CreateTablespace), count))
	case *parser.Load:
		return dispatched(executeLoad(e, op, stmt.(*parser.Load), count))
	case *parser.Vacuum:
		return dispatched(executeVacuum(e, scope, stmt.(*parser.Vacuum), count))
	case *parser.CreateExtension:
		return dispatched(executeExtension(e, scope, stmt.(*parser.CreateExtension), count))
	case *parser.DropExtension:
		return dispatched(executeDropExtension(e, scope, stmt.(*parser.DropExtension), count))
	case *parser.AlterExtensionUpdate:
		return dispatched(executeExtensionUpdate(e, scope, stmt.(*parser.AlterExtensionUpdate), count))
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return dispatched(executeRule(e, scope, stmt, count))
	case *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return dispatched(executePolicy(e, scope, stmt, count))
	case *parser.CreateIndex:
		return dispatched(executeCreateIndex(e, scope, stmt.(*parser.CreateIndex), count))
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return dispatched(executeIndexMaintenance(e, scope, stmt, count))
	case *parser.CreateView:
		return dispatched(executeCreateView(e, scope, stmt.(*parser.CreateView), count))
	case *parser.AlterOwner:
		return dispatched(executeOwnership(e, scope, stmt.(*parser.AlterOwner), count))
	case *parser.ReassignOwned, *parser.DropOwned:
		return dispatched(executeOwned(e, scope, stmt, count))
	case *parser.AlterView:
		if owner := stmt.(*parser.AlterView); owner.Action == "owner" {
			kind := "view"
			if owner.Materialized {
				kind = "materialized view"
			}
			return dispatched(core.ExecuteRelationOwnership(e, scope, owner.Name, owner.Target.Text, owner.OwnerIdentity, kind, owner.IfExists, count))
		}
		return dispatched(executeAlterView(e, scope, stmt.(*parser.AlterView), count))
	case *parser.DropView:
		return dispatched(executeDropView(e, scope, stmt.(*parser.DropView), count))
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return dispatched(executeSequenceDDL(e, scope, stmt, count))
	case *parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum:
		return dispatched(executeEnumDDL(e, scope, stmt, count))
	case *parser.AlterDomain:
		domain := stmt.(*parser.AlterDomain)
		var result core.Result
		var err error
		if domain.Action == parser.DomainRename || domain.Action == parser.DomainSetSchema {
			result, err = executeSchema(e, scope, op, stmt, count, scope.Input)
		} else {
			result, err = executeAlterDomain(e, scope, domain, count)
		}
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.AlterComposite:
		return dispatched(executeAlterComposite(e, scope, op, stmt.(*parser.AlterComposite), count))
	case *parser.Comment:
		if c := stmt.(*parser.Comment); c.Kind == parser.CommentSequence {
			return dispatched(executeSequenceComment(e, scope, c, count))
		}
		result, err := executeComment(e, scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateSchema, *parser.CreateLanguage, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.AlterType, *parser.DropType, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		result, err := executeSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		result, err := executeSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateCast, *parser.DropCast, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		result, err := executeSchema(e, scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateRole, *parser.AlterRole:
		result, err := executeRoleAttributes(e, scope, stmt, count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.ObjectGrant:
		grant := stmt.(*parser.ObjectGrant)
		if session.ForeignGrantKind(grant.Kind) {
			return dispatched(executeForeignGrant(e, scope, grant, count))
		}
		if grant.Kind == "database" {
			return dispatched(executeDatabaseGrant(e, scope, grant, count))
		}
		return dispatched(executeObjectGrant(e, scope, grant, count))
	case *parser.AlterDefaultPrivileges:
		return dispatched(executeDefaultPrivileges(e, scope, stmt.(*parser.AlterDefaultPrivileges), count))
	case *parser.DropRole, *parser.SessionAuthorization, *parser.RoleSelection, *parser.RoleMembership:
		if drop, ok := stmt.(*parser.DropRole); ok && count == 0 {
			if err := acquireRoleRetirementLocks(scope, drop); err != nil {
				return core.Result{}, true, err
			}
		}
		result, err := executeRole(e, op, stmt, count, !scope.Owner.InBlock())
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	}
	return core.Result{}, false, nil
}

func (familyStatements) ExecuteLocked(e *core.Engine, scope *core.StatementScope, auth core.AuthorizationView, stmt parser.Statement, count int) (core.Result, bool, error) {
	op := scope.Op
	switch s := stmt.(type) {
	case *parser.AlterTable:
		return dispatched(alterTableLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableDefault:
		return dispatched(alterDefaultLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableAllInTablespace:
		return dispatched(alterAllTablespaceLocked(e, scope, auth, s, count))
	case *parser.RefreshMaterializedView:
		return dispatched(refreshMaterializedLocked(e, scope, auth, s, count))
	case *parser.AlterTableOf:
		return dispatched(alterTableOfLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableAttachPartition:
		return dispatched(attachPartitionLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableDetachPartition:
		return dispatched(detachPartitionLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableRenameConstraint:
		return dispatched(renameConstraintLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableSetSchema:
		return dispatched(alterSchemaLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableRenameColumn:
		return dispatched(renameRelationLocked(e, scope, op, auth, s.Name, s.Column.Text, s.NewName.Text, s.IfExists, true, false, s.Only, count))
	case *parser.AlterTableRenameTable:
		return dispatched(renameRelationLocked(e, scope, op, auth, s.Name, "", s.NewName.Text, s.IfExists, false, false, false, count))
	case *parser.DropTable:
		return dispatched(dropTableLocked(e, scope, op, auth, s, count))
	case *parser.CreateTable:
		return dispatched(createTableLocked(e, scope, op, auth, s, count))
	}
	return core.Result{}, false, nil
}

// dispatched reports a statement as handled with its handler's outcome.
func dispatched(result core.Result, err error) (core.Result, bool, error) {
	return result, true, err
}
