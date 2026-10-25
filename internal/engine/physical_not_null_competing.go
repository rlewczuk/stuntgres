package engine

import (
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func PreparePhysicalCompetingUnique(e *core.Engine, op *session.Operation, raw *parser.AlterTable, provider *table.RestoredPhysicalForeignKeyProvider, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	if handled, err := table.CompetingUniqueShape(op, raw, provider); !handled || err != nil {
		return nil, err
	}
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !core.PartitionConstraintReplayImport(e, op, auth) || !provider.Holds(auth.RoleRecord, auth.Catalog) || created[provider.Ref.Address] != provider.Ref {
		return nil, core.SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	return table.PreparePhysicalCompetingUnique(auth.Catalog, raw, provider)
}
