package control

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
)

// RemapReplacementIdentities moves the candidate's colliding imported
// identities to their mapped addresses: default schemas, dump priorities,
// every descriptor and the references it holds to remapped objects, the name,
// operator and procedure indexes, and the storage and sequence roots keyed by
// address. current is the published catalog the caller holds under
// Engine.mu; exact immutable providers retained for temporary consumers are
// kept as they are. It does nothing when mapping is empty.
func RemapReplacementIdentities(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator, remap *catalog.RoutineRemap) error {
	if len(mapping) == 0 {
		return nil
	}
	catalog.RemapDefaultSchemas(next, mapping)
	capacity := next.DescriptorCount() + len(temporary)
	procedureNames := next.ProcedureNames()
	var descriptor func(catalog.Descriptor) catalog.Descriptor
	var procedure catalog.ProcedureRemapper
	if remap == nil {
		descriptor = extension.DescriptorRemapper(mapping)
		procedure = extension.ProcedureRemapper(descriptor, nil, nil)
	} else {
		descriptor, procedure = remap.Descriptor, remap.Procedure
		if descriptor == nil || procedure == nil {
			return fmt.Errorf("stuntgres: missing coherent replacement remap")
		}
	}
	originals := next.ResetMaps()
	next.BeginBulkLoad(capacity)
	err := func() error {
		defer next.EndBulkLoad()
		for a, d := range originals {
			target := a
			if dest, ok := mapping[a]; ok {
				target = dest
				priority := a.ObjectID
				if captured := next.DumpPriority(a); captured != 0 {
					priority = captured
					next.RemoveDumpPriority(a)
				}
				next.SetDumpPriority(target, priority)
			}
			remapped, err := remapReplacementDescriptor(current, mapping, a, target, d)
			if err != nil {
				return err
			}
			if p, ok := remapped.(*catalog.Procedure); ok {
				d, err = procedure(p)
				if err != nil {
					return err
				}
			} else {
				d = descriptor(remapped)
			}
			next.SetDescriptor(target, d)
			next.BindName(catalog.DescriptorNameKey(d), target)
		}
		return nil
	}()
	if err != nil {
		return err
	}
	next.SetOperatorNames(catalog.RebuildOperatorNames(next.Descriptors()))
	procedureIndex := catalog.RebuildProcedureNames(next.Descriptors())
	if procedureNames != nil {
		// OID remapping changes membership addresses, not public spellings.
		// Retain vacated-provider tombstones so registry fallback cannot
		// resurrect a declaration whose public identity was altered.
		procedureIndex.ProviderOverrides = maps.Clone(procedureNames.ProviderOverrides)
	}
	next.SetProcedureNames(procedureIndex)
	if err := remapInitialPrivilegeAddresses(next, mapping); err != nil {
		return err
	}
	remapStorageRoots(mapping, relations, sequences)
	return nil
}

// remapReplacementDescriptor relocates descriptor kinds owned by control.
// The caller delegates extension/catalog-owned descriptors to extension.RemapDescriptor.
func remapReplacementDescriptor(current catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, a, target ids.ObjectAddress, d catalog.Descriptor) (catalog.Descriptor, error) {
	switch r := d.(type) {
	case *catalog.Publication:
		n := *r
		n.ObjectAddress = target
		d = &n
	case *catalog.PublicationRelation:
		n := *r
		n.ObjectAddress = target
		if dest, ok := mapping[r.Publication]; ok {
			n.Publication = dest
		}
		if dest, ok := mapping[r.Relation]; ok {
			n.Relation = dest
		}
		dependencies := r.FilterDependencies()
		for i, dependency := range dependencies {
			whole := dependency
			whole.SubID = 0
			if dest, ok := mapping[whole]; ok {
				dest.SubID = dependency.SubID
				dependencies[i] = dest
			}
		}
		n.SetFilterDependencies(dependencies)
		d = &n
	case *catalog.PublicationNamespace:
		n := *r
		n.ObjectAddress = target
		if dest, ok := mapping[r.Publication]; ok {
			n.Publication = dest
		}
		if dest, ok := mapping[r.Schema]; ok {
			n.Schema = dest
		}
		d = &n
	case *catalog.Subscription:
		if r == current.Get(a) && r.Database != current.Owner().DatabaseAddress() {
			break
		}
		n := *r
		n.ObjectAddress = target
		n.Database = current.Owner().DatabaseAddress()
		d = &n
	case *catalog.ForeignObject:
		n := *r
		n.ObjectAddress = target
		move := func(address ids.ObjectAddress) ids.ObjectAddress {
			if destination, ok := mapping[address]; ok {
				return destination
			}
			return address
		}
		n.Wrapper, n.Server = move(r.Wrapper), move(r.Server)
		n.Handler, n.Validator = move(r.Handler), move(r.Validator)
		if n.Address().ClassID == ids.ForeignMappingClassID {
			n.ObjectName = catalog.ForeignMappingName(n.Server, n.MappedRole)
		}
		if target != a || n.Wrapper != r.Wrapper || n.Server != r.Server || n.Handler != r.Handler || n.Validator != r.Validator || n.ObjectName != r.ObjectName {
			d = &n
		}
	case *catalog.Namespace:
		if target == a && d == current.State().Get(a) && stored.DependPinned(current, nil, a) {
			// Preflight retained this exact immutable namespace authority.
			break
		}
		n := *r
		n.ObjectAddress = target
		d = &n
	case *catalog.Collation:
		n := *r
		n.OID = target.ObjectID
		if dest, ok := mapping[n.Namespace()]; ok {
			n.ObjectNamespace = dest
		}
		d = &n
	case *catalog.Statistics:
		n := *r
		n.ObjectAddress = target
		if dest, ok := mapping[n.Namespace()]; ok {
			n.ObjectNamespace = dest
		}
		d = &n
	case *catalog.Conversion:
		namespace, handler := r.Namespace(), r.Handler
		if dest, ok := mapping[namespace]; ok {
			namespace = dest
		}
		if dest, ok := mapping[handler]; ok {
			handler = dest
		}
		if target == a && namespace == r.Namespace() && handler == r.Handler {
			break
		}
		if r.Seeded() && target != a {
			return nil, fmt.Errorf("stuntgres: pinned conversion identity collision")
		}
		n := *r
		n.OID, n.ObjectNamespace, n.Handler = target.ObjectID, namespace, handler
		d = &n
	case *catalog.AccessMethod:
		n := *r
		n.ObjectAddress = target
		if dest, ok := mapping[ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: n.Handler}]; ok {
			n.Handler = dest.ObjectID
		}
		if catalog.AccessMethodSeeded(r) && target != a {
			return nil, fmt.Errorf("stuntgres: pinned access method identity collision")
		}
		if n != *r {
			d = &n
		}
	case *catalog.Relation:
		return remapReplacementRelation(current, mapping, a, target, r)
	}
	return d, nil
}

// remapStorageRoots re-keys the storage and sequence roots of remapped
// relations and sequences.
func remapStorageRoots(mapping map[ids.ObjectAddress]ids.ObjectAddress, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator) {
	// Copy into fresh maps before deleting old keys to avoid remap collisions.
	oldStorage := make(map[ids.ObjectAddress]*storage.Relation, len(relations))
	for a, s := range relations {
		oldStorage[a] = s
		delete(relations, a)
	}
	for a, s := range oldStorage {
		if dest, ok := mapping[a]; ok {
			a = dest
		}
		relations[a] = s
	}
	oldSequences := make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(sequences))
	for a, g := range sequences {
		oldSequences[a] = g
		delete(sequences, a)
	}
	for a, g := range oldSequences {
		if dest, ok := mapping[a]; ok {
			a = dest
		}
		sequences[a] = g
	}
}
