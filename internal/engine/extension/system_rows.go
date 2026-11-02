package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// SystemRowsProvider owns the released internal handler declaration. It is not
// a SQL-callable C ABI: analysis obtains a checked logical sampling capability.
type SystemRowsProvider struct{ control catalog.ExtensionControl }

var StockSystemRows = &SystemRowsProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Trusted: true, Relocatable: true,
	Comment: new("TABLESAMPLE method which accepts number of rows as a limit"),
}}

func (p *SystemRowsProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *SystemRowsProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func (p *SystemRowsProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *SystemRowsProvider) ValidateProcedure(v catalog.Read, procedure *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, procedure, p)
}
func (p *SystemRowsProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockSystemRows || !currentManifest(v, e) || e.Name() != "tsm_system_rows" || e.Version != "1.0" || len(e.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid system_rows manifest")
	}
	procedure, ok := v.Procedure(e.Members[0])
	if !ok {
		return fmt.Errorf("stuntgres: missing system_rows handler")
	}
	return p.ValidateProcedure(v, procedure)
}

// SystemRowsCapability authenticates the current installed declaration, not its
// mutable SQL name, result shape, or a claimed extension membership.
func SystemRowsCapability(v catalog.Read, procedure *catalog.Procedure) error {
	if procedure == nil || procedure.Implementation != StockSystemRows {
		return fmt.Errorf("stuntgres: invalid sampling handler capability")
	}
	if err := StockSystemRows.ValidateProcedure(v, procedure); err != nil {
		return err
	}
	symbol, err := ProcedureSymbol(v, procedure)
	if err != nil {
		return err
	}
	if symbol != "tsm_system_rows_handler" {
		return fmt.Errorf("stuntgres: invalid sampling recipe")
	}
	return nil
}

func (p *SystemRowsProvider) Stage(stage Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	procedure := &catalog.Procedure{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid},
		ObjectNamespace: e.TargetNamespace, Extension: e.Address(), ObjectRevision: revision,
		ObjectName: "system_rows", Signature: "2281", Result: 3310,
		RoutineKind: catalog.FunctionRoutine, Owner: owner, Strict: true, Volatility: expr.Volatile,
		Installed:      catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display:        catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "tsm_system_rows_handler", Binary: "$libdir/tsm_system_rows", HasBinary: true},
		Implementation: p,
	}
	if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "system_rows" already exists with same argument types`}
	}
	if err := stage.Create(procedure); err != nil {
		return err
	}
	e.Members = []ids.ObjectAddress{procedure.Address()}
	return nil
}
