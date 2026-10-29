package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// AutoincProvider owns the released declaration, not PostgreSQL's C loader.
type AutoincProvider struct{ control catalog.ExtensionControl }

var StockAutoinc = &AutoincProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Relocatable: true,
	Comment: new("functions for autoincrementing fields"),
}}

func (p *AutoincProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *AutoincProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func AutoincProviderOf(p *catalog.Procedure) *AutoincProvider {
	if p == nil { return nil }
	provider, _ := p.Implementation.(*AutoincProvider)
	return provider
}
func (p *AutoincProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockAutoinc || !currentManifest(v, ext) || ext.Name() != "autoinc" || ext.Version != "1.0" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid autoinc manifest")
	}
	d, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing autoinc procedure") }
	return p.ValidateProcedure(v, d)
}
func (p *AutoincProvider) ValidateProcedure(v catalog.Read, d *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, d, p)
}

// SupportsDMLTrigger exposes native body capability after ordinary current
// provider admission. Public spelling and membership never grant capability.
func (p *AutoincProvider) SupportsDMLTrigger(d *catalog.Procedure) bool {
	return p == StockAutoinc && d != nil && d.Implementation == p &&
		d.Origin.Valid(d.Address()) && d.Origin.Installation.Implementation == p &&
		d.Origin.Declaration == 0 && d.RoutineKind == catalog.FunctionRoutine &&
		d.Result == 2279 && d.Signature == "" && d.SQL == nil && d.Builtin == nil
}

func (p *AutoincProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}

func ValidateAutoincIdentity(v catalog.Read, d *catalog.Procedure, ref ids.ObjectRef) error {
	actual, ok := v.Procedure(ref.Address)
	if !ok || v.Owner() == nil || actual != d || ref.Domain() != v.Owner().Domain || ref.Epoch != v.Epoch() || AutoincProviderOf(d) != StockAutoinc {
		return fmt.Errorf("stuntgres: invalid stored autoinc identity")
	}
	return StockAutoinc.ValidateProcedure(v, d)
}
