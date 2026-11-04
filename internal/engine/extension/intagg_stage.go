package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// The enumerator's set capability is immutable provider identity, not a
// consequence of a mutable public row estimate.
func intaggDeclaration(slot int) catalog.InstalledRoutineDeclaration {
	if slot == 3 {
		return catalog.InstalledRoutineDeclaration{ReturnsSet: true, Rows: 1000, Parallel: 's'}
	}
	return catalog.InstalledRoutineDeclaration{Parallel: 's'}
}

// Stage installs the released helper aliases, aggregate and enumerator as
// genuine extension member procedures, allocating identities in script order.
func (p *IntaggProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, len(IntaggMembers))
	for i, member := range IntaggMembers {
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
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: member.Signature, RoutineKind: member.Kind, Owner: owner, Result: member.Result, Volatility: member.Volatility, Strict: member.Strict, Cost: 1, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: member.Source}, Implementation: p}
		proc.Installed = intaggDeclaration(i)
		if i == 2 {
			state, final := ext.Members[0].ObjectID, ext.Members[1].ObjectID
			proc.Aggregate = &catalog.OwnedAggregate{
				Definition: IntaggDefinition(oid, state, final),
				Helpers: [2]catalog.RoutineIdentity{
					{state, ext.TargetNamespace.ObjectID, IntaggMembers[0].Name, IntaggMembers[0].Signature},
					{final, ext.TargetNamespace.ObjectID, IntaggMembers[1].Name, IntaggMembers[1].Signature},
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
