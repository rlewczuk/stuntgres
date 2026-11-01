package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

type SSLInfoProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *SSLInfoProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *SSLInfoProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.2"}}
}

var StockSSLInfo = &SSLInfoProvider{control: catalog.ExtensionControl{Version: "1.2", Superuser: true, Relocatable: true, Comment: new("information about SSL certificates")}}

func (p *SSLInfoProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid sslinfo manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if p != StockSSLInfo || ext.Name() != "sslinfo" || ext.Version != p.control.Version || len(ext.Members) != len(functions.SSLInfoMembers) {
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

func (p *SSLInfoProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, proc, p)
}
