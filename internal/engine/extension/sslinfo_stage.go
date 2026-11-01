package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// Stage installs the sslinfo functions into stage, owned by owner,
// allocating identities in member order.
func (p *SSLInfoProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	ext.Members = make([]ids.ObjectAddress, 0, len(functions.SSLInfoMembers))
	for _, member := range functions.SSLInfoMembers {
		oid, err := stage.AllocateOID()
		if err != nil {
			return err
		}
		revision, err := stage.Revision()
		if err != nil {
			return err
		}
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		declaration := catalog.InstalledRoutineDeclaration{Parallel: 'r'}
		proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: member.Name, Signature: "", Owner: owner, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(member.Result), Volatility: expr.Volatile, Strict: true, Installed: declaration, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: member.Name}, Implementation: p}
		proc.Display.Binary, proc.Display.HasBinary = "$libdir/sslinfo", true
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
