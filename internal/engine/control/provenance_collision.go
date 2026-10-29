package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ProvenanceCollisionMapping reserves logical addresses that have no live
// descriptor: a dropped installation cohort or deleted recipe declaration may
// otherwise alias a retained TEMP provider after import. These reservations are
// only remap planning, never descriptors or dependency edges.
func ProvenanceCollisionMapping(next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress, allocate func(ids.OID) (ids.OID, error)) (map[ids.ObjectAddress]ids.ObjectAddress, error) {
	occupied := make(map[ids.ObjectAddress]bool, next.DescriptorCount()+len(temporary))
	for a := range next.Descriptors() {
		if n, ok := mapping[a]; ok {
			a = n
		}
		occupied[a] = true
	}
	for a := range temporary {
		occupied[a] = true
	}
	reserve := func(a ids.ObjectAddress) error {
		if a.ObjectID == 0 || next.Get(a) != nil {
			return nil
		}
		if _, mapped := mapping[a]; mapped {
			return nil
		}
		if !occupied[a] {
			return nil
		}
		oid, err := allocate(0)
		if err != nil {
			return err
		}
		if mapping == nil {
			mapping = make(map[ids.ObjectAddress]ids.ObjectAddress)
		}
		n := a
		n.ObjectID = oid
		mapping[a] = n
		occupied[n] = true
		return nil
	}
	seen := make(map[*catalog.Installation]bool)
	for _, d := range next.Descriptors() {
		i, ok := catalog.OriginInstallation(d)
		if e, extension := d.(*catalog.Extension); extension {
			i = e.Installation
			ok = i != nil
		}
		if !ok || seen[i] {
			continue
		}
		seen[i] = true
		if err := reserve(i.Cohort); err != nil {
			return nil, err
		}
		for _, a := range i.Members {
			if err := reserve(a); err != nil {
				return nil, err
			}
		}
	}
	return mapping, nil
}
