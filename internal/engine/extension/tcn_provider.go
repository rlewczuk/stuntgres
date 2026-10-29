package extension

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// TcnProvider owns the stock trigger declaration; names never grant capability.
type TcnProvider struct{ control catalog.ExtensionControl }

var StockTcn = &TcnProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Trusted: true, Relocatable: true,
	Comment: new("Triggered change notifications"),
}}

func (p *TcnProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *TcnProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func TcnProviderOf(d *catalog.Procedure) *TcnProvider {
	p, _ := d.Implementation.(*TcnProvider)
	return p
}
func (p *TcnProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *TcnProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}
func (p *TcnProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockTcn || !currentManifest(v, e) || e.Name() != "tcn" || e.Version != "1.0" || len(e.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid tcn manifest")
	}
	d, ok := v.Procedure(e.Members[0])
	if !ok {
		return fmt.Errorf("stuntgres: invalid tcn declaration")
	}
	_, err := ProcedureSymbol(v, d)
	return err
}
func (p *TcnProvider) SupportsDMLTrigger(d *catalog.Procedure) bool {
	return p == StockTcn && d != nil && d.Implementation == p && d.Origin.Valid(d.Address()) && d.Origin.Declaration == 0 && d.RoutineKind == catalog.FunctionRoutine && d.Signature == "" && d.Result == 2279 && d.Volatility == expr.Volatile && !d.Strict && d.SQL == nil && d.Builtin == nil
}
func (p *TcnProvider) Stage(stage Stage, e *catalog.Extension, owner authorization.PrincipalRef) error {
	oid, err := stage.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := stage.Revision()
	if err != nil {
		return err
	}
	d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: e.TargetNamespace, Extension: e.Address(), ObjectRevision: revision, ObjectName: "triggered_change_notification", RoutineKind: catalog.FunctionRoutine, Owner: owner, Result: 2279, Volatility: expr.Volatile, Installed: catalog.InstalledRoutineDeclaration{Parallel: 'u'}, Display: catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "triggered_change_notification", Binary: "$libdir/tcn", HasBinary: true}, Implementation: p}
	if stage.LookupName(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42723", Message: fmt.Sprintf("function %q already exists with same argument types", d.Name())}
	}
	if err := stage.Create(d); err != nil {
		return err
	}
	e.Members = []ids.ObjectAddress{d.Address()}
	return nil
}
