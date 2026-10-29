package catalog

import "stuntgres/internal/parser"

// EffectiveReplicaIdentity preserves the kind-dependent catalog default.
func (r *Relation) EffectiveReplicaIdentity() parser.ReplicaIdentityMode {
	if r.ReplicaIdentity != 0 {
		return r.ReplicaIdentity
	}
	switch r.RelationKind {
	case TableRelation, PartitionedRelation, MaterializedViewRelation:
		return parser.ReplicaIdentityDefault
	default:
		return parser.ReplicaIdentityNothing
	}
}

// ReplicaIdentityKey uses stable key attributes, never INCLUDE attributes.
func ReplicaIdentityKey(view Read, parent *Relation, attribute int32) bool {
	for _, descriptor := range view.Descriptors() {
		r, ok := descriptor.(*Relation)
		if !ok || r.SQLIndex == nil || r.SQLIndex.Parent != parent.Address() || !r.SQLIndex.ReplicaIdentity {
			continue
		}
		if backing := r.SQLIndex.Constraint; backing != nil {
			// DROP runs before MISC; an earlier constraint removal has already
			// retired its backing selection even before the drop closure is staged.
			retained := false
			for _, c := range parent.Constraints.Unique {
				retained = retained || c.OID == backing.OID
			}
			for _, c := range parent.Constraints.Exclusion {
				retained = retained || c.OID == backing.OID
			}
			if !retained {
				continue
			}
		}
		for _, key := range r.SQLIndex.Keys {
			if key.Attribute == attribute {
				return true
			}
		}
	}
	return false
}
