package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Stage installs the uuid-ossp functions into stage, owned by owner,
// allocating identities in member order.
func (p *UUIDOsspProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 0, len(functions.UUIDOsspMembers))
	for _, member := range functions.UUIDOsspMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: catalog.ProcedureSignature(member.Signature), Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2950, Volatility: member.Volatility, Strict: true, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/uuid-ossp", true
		if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
		}
		if err := stage.Create(proc); err != nil {
			return err
		}
		ext.Members = append(ext.Members, address)
	}
	return nil
}
