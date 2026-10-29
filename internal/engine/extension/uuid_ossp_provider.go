package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

type UUIDOsspProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *UUIDOsspProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *UUIDOsspProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var StockUUIDOssp = &UUIDOsspProvider{control: catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("generate universally unique identifiers (UUIDs)")}}

func (p *UUIDOsspProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid uuid-ossp manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockUUIDOssp || ext.Name() != "uuid-ossp" || ext.Version != p.control.Version || len(ext.Members) != len(functions.UUIDOsspMembers) {
		return invalid()
	}
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}

func (p *UUIDOsspProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
