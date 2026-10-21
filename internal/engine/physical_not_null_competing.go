package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) preparePhysicalCompetingUnique(op *session.Operation, raw *parser.AlterTable, provider *RestoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	if handled, err := CompetingUniqueShape(op, raw, provider); !handled || err != nil {
		return nil, err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !partitionConstraintReplayImport(e, op, auth) || !provider.Holds(auth.RoleRecord, auth.Catalog) || created[provider.Ref.Address] != provider.Ref {
		return nil, sqlError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return PreparePhysicalCompetingUnique(auth.Catalog, raw, provider)
}
