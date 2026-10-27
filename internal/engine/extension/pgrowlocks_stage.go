package extension

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

var pgrowlocksOutputs = catalog.NewRoutineOutputs([]ids.OID{27, 28, 16, 1011, 1009, 1007},
	"relname", "locked_row", "locker", "multi", "xids", "modes", "pids")

func pgrowlocksDeclaration() catalog.InstalledRoutineDeclaration {
	return catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: pgrowlocksOutputs}
}

func (p *PgrowlocksProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	proc := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision, ObjectName: "pgrowlocks", Signature: "25", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2249, Volatility: expr.Volatile, Strict: true, Installed: pgrowlocksDeclaration(), Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "pgrowlocks"}, Implementation: p}
	proc.Display.Binary, proc.Display.HasBinary = "$libdir/pgrowlocks", true
	if stage.LookupName(catalog.DescriptorNameKey(proc)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", proc.Name())}
	}
	if err := stage.Create(proc); err != nil {
		return err
	}
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
