package control

import (
	"fmt"

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
func RemapReplacementIdentities(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, relations map[ids.ObjectAddress]*storage.Relation, sequences map[ids.ObjectAddress]*storage.SequenceGenerator) error {
	if len(mapping) == 0 {
		return nil
	}
	catalog.RemapDefaultSchemas(next, mapping)
	originals := next.ResetMaps(next.DescriptorCount() + len(temporary))
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
		d = extension.RemapDescriptor(remapped, mapping)
		next.SetDescriptor(target, d)
		next.BindName(catalog.DescriptorNameKey(d), target)
	}
	next.SetOperatorNames(catalog.RebuildOperatorNames(next.Descriptors()))
	next.SetProcedureNames(catalog.RebuildProcedureNames(next.Descriptors()))
	remapStorageRoots(mapping, relations, sequences)
	return nil
}

// remapReplacementDescriptor returns d with its own address and its
// references moved through mapping; target is d's new address.
func remapReplacementDescriptor(current catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, a, target ids.ObjectAddress, d catalog.Descriptor) (catalog.Descriptor, error) {
	switch r := d.(type) {
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
	case *catalog.OperatorClass:
		d = catalog.RemapOperatorClass(r, mapping)
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		d = catalog.RemapOperatorFamily(r, mapping)
	case *catalog.Operator:
		n := *r
		n.ObjectAddress = target
		move := func(class, id ids.OID) ids.OID {
			if mapped, ok := mapping[ids.ObjectAddress{ClassID: class, ObjectID: id}]; ok {
				return mapped.ObjectID
			}
			return id
		}
		if mapped, ok := mapping[n.Namespace()]; ok {
			n.ObjectNamespace = mapped
		}
		n.Left, n.Right, n.Result = move(ids.TypeClassID, n.Left), move(ids.TypeClassID, n.Right), move(ids.TypeClassID, n.Result)
		n.Function, n.Restriction, n.Join = move(ids.ProcedureClassID, n.Function), move(ids.ProcedureClassID, n.Restriction), move(ids.ProcedureClassID, n.Join)
		n.Commutator, n.Negator = move(ids.OperatorClassID, n.Commutator), move(ids.OperatorClassID, n.Negator)
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
