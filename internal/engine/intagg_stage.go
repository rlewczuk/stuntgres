package engine

import (
	"fmt"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// intaggInstaller stages the intagg provider for the engine. Installed
// members hold the bare provider, never the installer.
type intaggInstaller struct{ *extension.IntaggProvider }

func (p intaggInstaller) Stage(stage extension.Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, len(extension.IntaggMembers))
	for i, member := range extension.IntaggMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		ext.Members[i] = address
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: member.Kind, Owner: owner, Result: member.Result, Volatility: expr.Immutable, Strict: member.Strict, Implementation: p.IntaggProvider}
		if i == 0 {
			state, err := stage.AllocateOID()
			if err != nil {
				return err
			}
			final, err := stage.AllocateOID()
			if err != nil {
				return err
			}
			proc.Aggregate = &catalog.OwnedAggregate{
				Definition: extension.IntaggDefinition(oid, state, final),
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
