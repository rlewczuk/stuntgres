package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (p *ModdatetimeProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "moddatetime", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2279, Volatility: expr.Volatile, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "moddatetime", Binary: "$libdir/moddatetime", HasBinary: true}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	if err := stage.Create(proc); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
