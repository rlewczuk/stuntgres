package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (p *intaggProvider) stage(e *Engine, stage *CatalogStage, ext *catalog.Extension, owner authorization.PrincipalRef, auth AuthorizationView) error {
	ext.Members = make([]ids.ObjectAddress, len(intaggMembers))
	for i, member := range intaggMembers {
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members[i] = address
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.name, Signature: member.signature, RoutineKind: member.kind, Owner: owner, Result: member.result, Volatility: expr.Immutable, Strict: member.strict, Implementation: p}
		if i == 0 {
			state, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			final, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			proc.Aggregate = &catalog.OwnedAggregate{
				Definition: intaggDefinition(oid, state, final),
				Helpers: [2]catalog.RoutineIdentity{
					{state, ext.TargetNamespace.ObjectID, "int_agg_state", "2281,23"},
					{final, ext.TargetNamespace.ObjectID, "int_agg_final_array", "2281"},
				},
			}
		}
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
	}
	return nil
}
