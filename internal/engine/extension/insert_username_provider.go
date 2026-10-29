package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type InsertUsernameProvider struct{ control catalog.ExtensionControl }

var StockInsertUsername = &InsertUsernameProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Relocatable: true,
	Comment: new("functions for tracking who changed a table"),
}}

func (p *InsertUsernameProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *InsertUsernameProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func (p *InsertUsernameProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *InsertUsernameProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}
func (p *InsertUsernameProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockInsertUsername || !currentManifest(v, e) || e.Name() != "insert_username" || e.Version != "1.0" || len(e.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid insert_username manifest")
	}
	d, ok := v.Procedure(e.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing insert_username declaration") }
	return p.ValidateProcedure(v, d)
}

// SupportsDMLTrigger classifies the native body, never grants executable authority.
func (p *InsertUsernameProvider) SupportsDMLTrigger(d *catalog.Procedure) bool {
	return p == StockInsertUsername && d != nil && d.Implementation == p &&
		d.Origin.Valid(d.Address()) && d.Origin.Installation.Implementation == p &&
		d.Origin.Declaration == 0 && d.SQL == nil && d.Builtin == nil &&
		d.RoutineKind == catalog.FunctionRoutine && d.Result == 2279 && d.Signature == ""
}

func (p *InsertUsernameProvider) Stage(stage Stage, ext *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil { return err }
	revision, err := stage.Revision()
	if err != nil { return err }
	address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
	d := &catalog.Procedure{
		ObjectAddress: address, ObjectNamespace: ext.TargetNamespace, Extension: ext.Address(),
		ObjectRevision: revision, ObjectName: "insert_username", RoutineKind: catalog.FunctionRoutine,
		Owner: owner, Result: 2279, Volatility: expr.Volatile,
		Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'},
		Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "insert_username", Binary: "$libdir/insert_username", HasBinary: true},
		Implementation: p,
	}
	if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", d.Name())}
	}
	if err := stage.Create(d); err != nil { return err }
	ext.Members = []ids.ObjectAddress{address}
	return nil
}
