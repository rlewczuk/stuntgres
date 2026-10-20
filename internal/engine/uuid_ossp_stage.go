package engine

import (
	"fmt"
	"stuntgres/internal/engine/extension"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// uuidOsspInstaller stages the uuid-ossp provider for the engine. Installed
// members hold the bare provider, never the installer.
type uuidOsspInstaller struct{ *extension.UUIDOsspProvider }

func (p uuidOsspInstaller) Stage(stage extension.Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
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
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: "", Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: 2950, Volatility: member.Volatility, Strict: true, Implementation: p.UUIDOsspProvider}
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
