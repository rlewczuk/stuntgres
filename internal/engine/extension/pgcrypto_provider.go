package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

type PgcryptoProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *PgcryptoProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *PgcryptoProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.4"}}
}

var StockPgcrypto = &PgcryptoProvider{control: catalog.ExtensionControl{Version: "1.4", Trusted: true, Superuser: true, Relocatable: true, Comment: new("cryptographic functions")}}

func (p *PgcryptoProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid pgcrypto manifest") }
	if !currentManifest(v, ext) {
		return invalid()
	}
	if v.Owner() == nil || p != StockPgcrypto || ext.Name() != "pgcrypto" || ext.Version != p.control.Version || len(ext.Members) != len(functions.PgcryptoMembers) {
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

func (p *PgcryptoProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if _, err := ProcedureSymbol(v, proc); err != nil {
		return err
	}
	if v.Get(proc.Address()) != proc || PgcryptoProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: invalid pgcrypto procedure identity")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgcrypto installation")
	}
	return p.ValidateExtension(v, ext)
}
