package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// A keyless physical cleanup may be followed immediately by the captured,
// unrelated ordinary primary declaration. It cannot certify one of its own
// flag-only attributes as a primary key (or as an INCLUDE payload key).
func (e *Engine) prepareDeferredPhysicalPrimary(op *session.Operation, raw *parser.AlterTable, pair *table.RestoredPhysicalNotNull, created map[ids.ObjectAddress]ids.ObjectRef) (*table.PhysicalNotNullConstructor, error) {
	if handled, err := table.DeferredPhysicalPrimaryShape(op, raw, pair); !handled || err != nil {
		return nil, err
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if !PartitionConstraintReplayImport(e, op, auth) || !pair.Holds(auth.RoleRecord, auth.Catalog) || created[pair.Ref.Address] != pair.Ref || pair.Ref.Epoch != auth.Catalog.Epoch() {
		return nil, SQLError("0A000", table.RestorePhysicalNotNullBoundary)
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	return table.PrepareDeferredPhysicalPrimary(auth.Catalog, ref, raw, pair)
}
