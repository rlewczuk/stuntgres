package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// familyStatements is the composition's Statements: the family cases of both
// dispatch switches, in their historical order.
type familyStatements struct{}

var _ Statements = familyStatements{}

func (familyStatements) Execute(e *Engine, scope *StatementScope, stmt parser.Statement, count int) (Result, bool, error) {
	op := scope.Op
	switch stmt.(type) {
	case *parser.CreateTrigger, *parser.AlterTrigger, *parser.DropTrigger:
		return dispatched(e.executeTrigger(scope, stmt, count))
	case *parser.CreateFunction, *parser.CreateAggregate:
		return dispatched(e.executeSQLRoutineDDL(scope, stmt, count))
	case *parser.ForeignDDL:
		result, err := e.executeForeignDDL(scope, stmt.(*parser.ForeignDDL), count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateDatabaseStmt:
		return dispatched(e.executeCreateDatabase(scope, stmt.(*parser.CreateDatabaseStmt), count))
	case *parser.CreateTablespace:
		return dispatched(e.executeCreateTablespace(op, stmt.(*parser.CreateTablespace), count))
	case *parser.Load:
		return dispatched(e.executeLoad(op, stmt.(*parser.Load), count))
	case *parser.Vacuum:
		return dispatched(e.executeVacuum(scope, stmt.(*parser.Vacuum), count))
	case *parser.CreateExtension:
		return dispatched(e.executeExtension(scope, stmt.(*parser.CreateExtension), count))
	case *parser.DropExtension:
		return dispatched(e.executeDropExtension(scope, stmt.(*parser.DropExtension), count))
	case *parser.AlterExtensionUpdate:
		return dispatched(e.executeExtensionUpdate(scope, stmt.(*parser.AlterExtensionUpdate), count))
	case *parser.CreateRule, *parser.AlterRule, *parser.DropRule:
		return dispatched(e.executeRule(scope, stmt, count))
	case *parser.CreatePolicy, *parser.AlterPolicy, *parser.DropPolicy:
		return dispatched(e.executePolicy(scope, stmt, count))
	case *parser.CreateIndex:
		return dispatched(e.executeCreateIndex(scope, stmt.(*parser.CreateIndex), count))
	case *parser.DropIndex, *parser.AlterIndex, *parser.Reindex:
		return dispatched(e.executeIndexMaintenance(scope, stmt, count))
	case *parser.CreateView:
		return dispatched(e.executeCreateView(scope, stmt.(*parser.CreateView), count))
	case *parser.AlterOwner:
		return dispatched(e.executeOwnership(scope, stmt.(*parser.AlterOwner), count))
	case *parser.ReassignOwned, *parser.DropOwned:
		return dispatched(e.executeOwned(scope, stmt, count))
	case *parser.AlterView:
		if owner := stmt.(*parser.AlterView); owner.Action == "owner" {
			kind := "view"
			if owner.Materialized {
				kind = "materialized view"
			}
			return dispatched(e.executeRelationOwnership(scope, owner.Name, owner.Target.Text, owner.OwnerIdentity, kind, owner.IfExists, count))
		}
		return dispatched(e.executeAlterView(scope, stmt.(*parser.AlterView), count))
	case *parser.DropView:
		return dispatched(e.executeDropView(scope, stmt.(*parser.DropView), count))
	case *parser.CreateSequence, *parser.AlterSequence, *parser.DropSequence:
		return dispatched(e.executeSequenceDDL(scope, stmt, count))
	case *parser.CreateRange, *parser.CreateEnum, *parser.AlterEnum:
		return dispatched(e.executeEnumDDL(scope, stmt, count))
	case *parser.AlterDomain:
		domain := stmt.(*parser.AlterDomain)
		var result Result
		var err error
		if domain.Action == parser.DomainRename || domain.Action == parser.DomainSetSchema {
			result, err = e.executeSchema(scope, op, stmt, count, scope.Input)
		} else {
			result, err = e.executeAlterDomain(scope, domain, count)
		}
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.AlterComposite:
		return dispatched(e.executeAlterComposite(scope, op, stmt.(*parser.AlterComposite), count))
	case *parser.Comment:
		if c := stmt.(*parser.Comment); c.Kind == parser.CommentSequence {
			return dispatched(e.executeSequenceComment(scope, c, count))
		}
		result, err := e.executeComment(scope, stmt.(*parser.Comment), count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateSchema, *parser.CreateLanguage, *parser.AlterSchema, *parser.DropSchema, *parser.CreateComposite, *parser.AlterType, *parser.DropType, *parser.CreateDomain, *parser.DropDomain, *parser.CreateCollation, *parser.AlterCollation, *parser.DropCollation, *parser.CreateConversion, *parser.AlterConversion, *parser.DropConversion:
		result, err := e.executeSchema(scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateAccessMethod, *parser.DropAccessMethod:
		result, err := e.executeSchema(scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateCast, *parser.DropCast, *parser.CreateOperator, *parser.AlterOperator, *parser.DropOperator, *parser.DropRoutine, *parser.AlterRoutine, *parser.CreateOperatorFamily, *parser.AlterOperatorFamily, *parser.DropOperatorFamily, *parser.CreateOperatorClass, *parser.AlterOperatorClass, *parser.DropOperatorClass:
		result, err := e.executeSchema(scope, op, stmt, count, scope.Input)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.CreateRole, *parser.AlterRole:
		result, err := e.executeRoleAttributes(scope, stmt, count)
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	case *parser.ObjectGrant:
		grant := stmt.(*parser.ObjectGrant)
		if session.ForeignGrantKind(grant.Kind) {
			return dispatched(e.executeForeignGrant(scope, grant, count))
		}
		if grant.Kind == "database" {
			return dispatched(e.executeDatabaseGrant(scope, grant, count))
		}
		return dispatched(e.executeObjectGrant(scope, grant, count))
	case *parser.AlterDefaultPrivileges:
		return dispatched(e.executeDefaultPrivileges(scope, stmt.(*parser.AlterDefaultPrivileges), count))
	case *parser.DropRole, *parser.SessionAuthorization, *parser.RoleSelection, *parser.RoleMembership:
		if drop, ok := stmt.(*parser.DropRole); ok && count == 0 {
			if err := scope.acquireRoleRetirementLocks(drop); err != nil {
				return Result{}, true, err
			}
		}
		result, err := e.executeRole(op, stmt, count, !scope.Owner.InBlock())
		if err == nil {
			scope.Completed = true
		}
		return result, true, err
	}
	return Result{}, false, nil
}

func (familyStatements) ExecuteLocked(e *Engine, scope *StatementScope, auth AuthorizationView, stmt parser.Statement, count int) (Result, bool, error) {
	op := scope.Op
	switch s := stmt.(type) {
	case *parser.AlterTable:
		return dispatched(alterTableLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableDefault:
		return dispatched(alterDefaultLocked(e, scope, op, auth, s, count))
	case *parser.AlterTableAllInTablespace:
		return dispatched(e.alterAllTablespaceLocked(scope, auth, s, count))
	case *parser.RefreshMaterializedView:
		return dispatched(e.refreshMaterializedLocked(scope, auth, s, count))
	case *parser.AlterTableOf:
		return dispatched(e.alterTableOfLocked(scope, op, auth, s, count))
	case *parser.AlterTableAttachPartition:
		return dispatched(e.attachPartitionLocked(scope, op, auth, s, count))
	case *parser.AlterTableDetachPartition:
		return dispatched(e.detachPartitionLocked(scope, op, auth, s, count))
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
	return Result{}, false, nil
}

// dispatched reports a statement as handled with its handler's outcome.
func dispatched(result Result, err error) (Result, bool, error) {
	return result, true, err
}
