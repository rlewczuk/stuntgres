package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type UUIDOsspProvider struct{ control catalog.ExtensionControl }

// Control implements catalog.ExtensionImplementation.
func (p *UUIDOsspProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *UUIDOsspProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.1"}}
}

var StockUUIDOssp = &UUIDOsspProvider{control: catalog.ExtensionControl{Version: "1.1", Trusted: true, Superuser: true, Relocatable: true, Comment: new("generate universally unique identifiers (UUIDs)")}}

func (p *UUIDOsspProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid uuid-ossp manifest") }
	if p != StockUUIDOssp || ext.Name() != "uuid-ossp" || ext.Version != p.control.Version || len(ext.Members) != len(functions.UUIDOsspMembers) {
		return invalid()
	}
	var seen [len(functions.UUIDOsspMembers)]bool
	for _, address := range ext.Members {
		proc, ok := v.Procedure(address)
		if !ok {
			return invalid()
		}
		memberIndex := -1
		for i, member := range functions.UUIDOsspMembers {
			if proc.Name() == member.Name && proc.Volatility == member.Volatility {
				memberIndex = i
				break
			}
		}
		if memberIndex < 0 || seen[memberIndex] || address.ClassID != ids.ProcedureClassID || address.ObjectID == 0 || address.SubID != 0 || proc.Address() != address || proc.Namespace() != ext.TargetNamespace || proc.Extension != ext.Address() || proc.Revision() == 0 || proc.RoutineKind != catalog.FunctionRoutine || proc.Builtin != nil || proc.Implementation != p || proc.Aggregate != nil || proc.Foreign != nil || proc.Conversion.Name() != "" || proc.Dictionary != (ids.ObjectAddress{}) || proc.Signature != "" || proc.Result != 2950 || !proc.Strict || proc.Owner == (authorization.PrincipalRef{}) || v.NameAddress(catalog.DescriptorNameKey(proc)) != address {
			return invalid()
		}
		seen[memberIndex] = true
	}
	return nil
}

func (p *UUIDOsspProvider) ValidateProcedure(v catalog.Read, proc *catalog.Procedure) error {
	if v.Get(proc.Address()) != proc || UUIDOsspProviderOf(proc) != p {
		return fmt.Errorf("stuntgres: invalid uuid-ossp procedure identity")
	}
	ext, ok := v.Extension(proc.Extension)
	if !ok {
		return fmt.Errorf("stuntgres: missing uuid-ossp installation")
	}
	return p.ValidateExtension(v, ext)
}
