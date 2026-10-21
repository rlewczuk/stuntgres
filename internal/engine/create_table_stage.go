package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (e *Engine) schemaTableLocked(op *session.Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateTable, ns ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) error {
	return StageCreateTable(ddlHost{e: e, op: op, stage: stage}, auth, s, ns, owner, input, foreignTargets)
}
