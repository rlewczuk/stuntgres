package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
)

type ModdatetimeProvider struct{ control catalog.ExtensionControl }

var StockModdatetime = &ModdatetimeProvider{control: catalog.ExtensionControl{
	Version: "1.0", Superuser: true, Relocatable: true,
	Comment: new("functions for tracking last modification time"),
}}

func (p *ModdatetimeProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *ModdatetimeProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.0"}}
}
func ModdatetimeProviderOf(p *catalog.Procedure) *ModdatetimeProvider {
	if p == nil { return nil }
	provider, _ := p.Implementation.(*ModdatetimeProvider)
	return provider
}
func (p *ModdatetimeProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func (p *ModdatetimeProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
func (p *ModdatetimeProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockModdatetime || !currentManifest(v, ext) || ext.Name() != "moddatetime" || ext.Version != "1.0" || len(ext.Members) != 1 {
		return fmt.Errorf("stuntgres: invalid moddatetime manifest")
	}
	proc, ok := v.Procedure(ext.Members[0])
	if !ok { return fmt.Errorf("stuntgres: missing moddatetime declaration") }
	return p.ValidateProcedure(v, proc)
}

// SupportsDMLTrigger describes the sealed released body, not current
// catalog authority. Firing must still validate the current routine descriptor.
func (p *ModdatetimeProvider) SupportsDMLTrigger(proc *catalog.Procedure) bool {
	if p != StockModdatetime || proc == nil || proc.Implementation != p || !proc.Origin.Valid(proc.Address()) { return false }
	origin := proc.Origin.Installation
	if origin.Implementation != p || origin.Name() != "moddatetime" || origin.Version != "1.0" || len(origin.Members) != 1 || proc.Origin.Declaration != 0 { return false }
	declaration, ok := origin.Declarations[0].(*catalog.Procedure)
	return ok && proc.RoutineKind == catalog.FunctionRoutine && proc.Signature == "" && proc.Result == 2279 && !proc.Strict && proc.Volatility == declaration.Volatility && proc.Display == declaration.Display && proc.Installed == declaration.Installed && proc.SQL == nil && proc.Builtin == nil && proc.Foreign == nil
}
