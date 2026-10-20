package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
	if v.Owner() == nil || p != StockPgcrypto || ext.Name() != "pgcrypto" || ext.Version != p.control.Version || len(ext.Members) != len(functions.PgcryptoMembers) {
		return invalid()
	}
	seen := make(map[functions.PgcryptoMember]bool, len(functions.PgcryptoMembers))
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		member, admitted := functions.PgcryptoLookup(proc.Name(), string(proc.Signature))
		if !admitted || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.Implementation != p || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Result != member.Result || proc.Volatility != member.Volatility || proc.Strict != member.Strict || seen[member] || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		// Extension membership fixes provider semantics, not independently
		// transferable member ownership. Each owner still has real provenance.
		if proc.Owner.ID() == 0 || proc.Owner != v.Owner().Principals.Principal(proc.Owner.ID()) {
			return invalid()
		}
		seen[member] = true
	}
	return nil
}

func (p *PgcryptoProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if v.Get(proc.Address()) != proc || PgcryptoProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: invalid pgcrypto procedure identity")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing pgcrypto installation")
	}
	return p.ValidateExtension(v, ext)
}
