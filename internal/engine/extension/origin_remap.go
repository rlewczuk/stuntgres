package extension

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func remapInstallation(i *catalog.Installation, mapping map[ids.ObjectAddress]ids.ObjectAddress, canonical func(catalog.Descriptor) catalog.Descriptor) *catalog.Installation {
	// Remapping must never turn an unsealed input into installation authority.
	// Retain its invalid provenance so the normal candidate validation rejects it.
	if i == nil || !i.Valid() {
		return i
	}
	cohort := i.Cohort
	if next, ok := mapping[cohort]; ok {
		cohort = next
	}
	var members []ids.ObjectAddress
	for n, a := range i.Members {
		next := a
		if mapped, ok := mapping[a]; ok {
			next = mapped
		}
		if next != a && members == nil {
			members = slices.Clone(i.Members)
		}
		if members != nil {
			members[n] = next
		}
	}
	var declarations []catalog.Descriptor
	for n, d := range i.Declarations {
		next := canonical(d)
		if next != d && declarations == nil {
			declarations = slices.Clone(i.Declarations)
		}
		if declarations != nil {
			declarations[n] = next
		}
	}
	if cohort == i.Cohort && members == nil && declarations == nil {
		return i
	}
	if members == nil {
		members = i.Members
	}
	if declarations == nil {
		declarations = i.Declarations
	}
	next := catalog.NewInstallation(cohort, i.Provider, i.Version, i.Implementation, members)
	next.Declarations = declarations
	return next
}

// DescriptorRemapper shares immutable installation recipes across one imported
// graph. Unchanged recipes/descriptors retain exact backing; no SQL labels or
// current member offsets participate in the remap.
func DescriptorRemapper(mapping map[ids.ObjectAddress]ids.ObjectAddress) func(catalog.Descriptor) catalog.Descriptor {
	if len(mapping) == 0 {
		return func(d catalog.Descriptor) catalog.Descriptor { return d }
	}
	installations := make(map[*catalog.Installation]*catalog.Installation)
	canonical := canonicalDeclarationRemapper(mapping)
	installation := func(i *catalog.Installation) *catalog.Installation {
		if i == nil {
			return nil
		}
		if next, ok := installations[i]; ok {
			return next
		}
		next := remapInstallation(i, mapping, canonical)
		installations[i] = next
		return next
	}
	return func(d catalog.Descriptor) catalog.Descriptor { return remapDescriptor(d, mapping, installation) }
}

// RemapDescriptor handles a single descriptor. Graph owners use the shared
// DescriptorRemapper so every declaration retains one immutable recipe backing.
func RemapDescriptor(d catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) catalog.Descriptor {
	if len(mapping) == 0 {
		return d
	}
	canonical := canonicalDeclarationRemapper(mapping)
	return remapDescriptor(d, mapping, func(i *catalog.Installation) *catalog.Installation { return remapInstallation(i, mapping, canonical) })
}
