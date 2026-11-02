package control

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// CompatibleInstallationIdentities selects live destinations for authentic
// imported headers and procedure recipe roots. Names locate the current header;
// only its validated sealed provider/release and declaration origins grant a
// match. Ordinary members and historical detached cohorts are never candidates.
// The caller holds the destination catalog lock and applies the returned fixed
// assignments through the existing collision planner and complete graph remap.
func CompatibleInstallationIdentities(current, imported catalog.Read) (map[ids.ObjectAddress]ids.ObjectAddress, error) {
	var fixed map[ids.ObjectAddress]ids.ObjectAddress
	keep := func(source, target ids.ObjectAddress) {
		if fixed == nil {
			fixed = make(map[ids.ObjectAddress]ids.ObjectAddress)
		}
		fixed[source] = target
	}
	for _, descriptor := range imported.Descriptors() {
		source, ok := descriptor.(*catalog.Extension)
		if !ok || !source.Installation.Valid() {
			continue
		}
		native := false
		for _, member := range source.Installation.Members {
			if procedure, ok := imported.Procedure(member); ok && procedure.Implementation != nil && procedure.Implementation == source.Installation.Implementation {
				native = true
				break
			}
		}
		if !native {
			// Bootstrap language handlers and other class-owned declarations
			// retain their existing replacement policy, not C-provider roles.
			continue
		}
		address, found := current.LookupName(catalog.DescriptorNameKey(source))
		if !found {
			continue
		}
		target, ok := current.Extension(address)
		if !ok || !target.Installation.Valid() || target.Installation.Implementation != source.Installation.Implementation || target.Version != source.Version || len(target.Installation.Members) != len(source.Installation.Members) {
			continue
		}
		if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(imported, source); err != nil {
			return nil, err
		}
		if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(current, target); err != nil {
			return nil, err
		}
		keep(source.Address(), target.Address())
		for slot, sourceAddress := range source.Installation.Members {
			a, sourceExists := imported.Procedure(sourceAddress)
			b, targetExists := current.Procedure(target.Installation.Members[slot])
			if !sourceExists || !targetExists || a.Implementation == nil || a.Implementation != b.Implementation || a.Signature != b.Signature || a.Result != b.Result || a.RoutineKind != b.RoutineKind || a.Origin.Installation.Provider != b.Origin.Installation.Provider || a.Origin.Installation.Version != b.Origin.Installation.Version || a.Origin.Declaration != b.Origin.Declaration {
				continue
			}
			// Exact released SQL/C roles, not a same-signature mutable label,
			// distinguish different native bodies within one provider recipe.
			name, err := extension.ProcedureInstallationName(imported, a)
			if err != nil {
				return nil, err
			}
			otherName, err := extension.ProcedureInstallationName(current, b)
			if err != nil {
				return nil, err
			}
			symbol, err := extension.ProcedureSymbol(imported, a)
			if err != nil {
				return nil, err
			}
			otherSymbol, err := extension.ProcedureSymbol(current, b)
			if err != nil {
				return nil, err
			}
			if name != otherName || symbol != otherSymbol {
				return nil, fmt.Errorf("stuntgres: incompatible installed procedure declaration roles")
			}
			keep(a.Address(), b.Address())
		}
	}
	return fixed, nil
}
