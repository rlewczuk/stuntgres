package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// SystemTimeProvider owns the released internal handler declaration. It is not
// a SQL-callable C ABI: analysis obtains a checked logical sampling capability.
type SystemTimeProvider struct{ control catalog.ExtensionControl }

var StockSystemTime = &SystemTimeProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Trusted: true, Relocatable: true,
	Comment: new("TABLESAMPLE method which accepts time in milliseconds as a limit"),
}}

func (p *SystemTimeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *SystemTimeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func (p *SystemTimeProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *SystemTimeProvider) ValidateProcedure(v catalog.Read, procedure *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, procedure, p)
}
func (p *SystemTimeProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockSystemTime || !currentManifest(v, e) || e.Name() != "tsm_system_time" || e.Version != "1.0" || len(e.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid system_time manifest")
	}
	procedure, ok := v.Procedure(e.Members[0])
	if !ok {
		return fmt.Errorf("stuntgres: missing system_time handler")
	}
	return p.ValidateProcedure(v, procedure)
}

func (p *SystemTimeProvider) Stage(stage Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
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
		ObjectName: "system_time", Signature: "2281", Result: 3310,
		RoutineKind: catalog.FunctionRoutine, Owner: owner, Strict: true, Volatility: expr.Volatile,
		Installed:      catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display:        catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "tsm_system_time_handler", Binary: "$libdir/tsm_system_time", HasBinary: true},
		Implementation: p,
	}
	if stage.LookupName(catalog.DescriptorNameKey(procedure)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: `function "system_time" already exists with same argument types`}
	}
	if err := stage.Create(procedure); err != nil {
		return err
	}
	e.Members = []ids.ObjectAddress{procedure.Address()}
	return nil
}
