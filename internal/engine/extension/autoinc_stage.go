package extension

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func (p *AutoincProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	d := &catalog.Procedure{ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(), ObjectRevision: revision,
		ObjectName: "autoinc", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2279, Volatility: expr.Volatile,
		Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "autoinc", Binary: "$libdir/autoinc", HasBinary: true}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "autoinc" already exists with same argument types`,
			Context: "SQL statement \"CREATE FUNCTION autoinc()\nRETURNS trigger\nAS '$libdir/autoinc'\nLANGUAGE C\"\nextension script file \"autoinc--1.0.sql\", near line 6"}
	}
	if err := stage.Create(d); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
