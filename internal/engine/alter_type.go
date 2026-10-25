package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Sequence AS uses admitted target usages, including the prefix retained when
// transformation completion fails. It never visits an unadmitted later TYPE.
func (e *Engine) stageAlterIdentityTypes(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, actions []parser.AlterTableAction, prepared []table.AlterTypePreparation) (*CatalogStage, error) {
	var stage *CatalogStage
	index := 0
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		if index == len(prepared) {
			break
		}
		slot := original.Index[action.Column.Text]
		attribute := original.Attributes[slot]
		if identity, exists := original.ColumnMeta.Identities[attribute]; exists {
			if stage == nil {
				if scope.Inheritance.Alter != nil {
					stage = scope.Inheritance.Alter.Stage
				} else {
					stage = e.BeginCatalogStage(auth.Catalog)
					stage.AuthorizeLocked(auth, op.Session)
				}
			}
			if err := e.alterIdentityTypeSequence(scope, stage, original, attribute, identity.Sequence, prepared[index].Usage); err != nil {
				return stage, err
			}
		}
		index++
	}
	return stage, nil
}

// prepareAlterTypeLocked admits and completes one TYPE action; the caller holds
// Engine.mu. suppliedUsage is used only by typed propagation of an already
// resolved type.
func (e *Engine) prepareAlterTypeLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, original *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, suppliedUsage *sqltype.Usage) (table.AlterTypePreparation, error) {
	return table.PrepareAlterType(alterHost{TableHost{DDLHost{E: e, Op: op}}, scope}, auth, original, ref, a, suppliedUsage)
}

// alterTypeCandidate runs the TYPE candidate under Engine.mu; stage, when
// begun, supplies the staged dependent views.
func (e *Engine) alterTypeCandidate(scope *StatementScope, op *session.Operation, auth AuthorizationView, stage *CatalogStage, original, next *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, prepared table.AlterTypePreparation) (*catalog.Relation, index.ColumnTransformation, error) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return table.AlterTypeCandidate(alterHost{TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, scope}, auth, original, next, ref, a, prepared)
}

// alterRowEvaluation is the statement's evaluation template for ALTER row
// fills, rewrites and scans; read is the catalog local lowering admits against.
func alterRowEvaluation(scope *StatementScope, op *session.Operation, read catalog.Read) exec.Context {
	return exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: HostOf(scope), Input: scope.Input, Output: scope.Output, Catalog: read}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
}
