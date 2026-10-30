package catalog

import "stuntgres/internal/engine/ids"

// A state's oidCeiling bounds the global OID space its descriptors use: no
// descriptor address outside the large-object class and no owned definition
// identity exceeds it. SetDescriptor and Candidate.State raise it; removals
// leave it, so it stays a bound, and the resets that empty the descriptor map
// clear it. An OID above the ceiling is unused, which lets allocation and
// definition-identity checks skip the catalog scan.

func (s *State) raiseOIDCeiling(a ids.ObjectAddress, d Descriptor) {
	if a.ClassID != ids.LargeObjectClassID && a.ClassID != ids.ReplicationOriginClassID && a.ObjectID > s.oidCeiling {
		s.oidCeiling = a.ObjectID
	}
	_ = VisitDefinitionOIDs(d, func(id ids.OID) error {
		if id > s.oidCeiling {
			s.oidCeiling = id
		}
		return nil
	})
}
