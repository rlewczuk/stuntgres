package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Ownership changes retain immutable type, row, index and sequence generator
// roots. Only descriptor/ACL payloads change. All reciprocal members are staged
// before the ordinary publication validator sees any of them.
func stageOwnership(stage ownerStage, d catalog.Descriptor, target authorization.PrincipalRef) error {
	if next := stage.Get(d.Address()); next != nil {
		d = next
	}
	owner, _, _ := access.DescriptorACL(d)
	if owner == target {
		return nil
	}
	revision, err := stage.CascadeRevision(d.Revision())
	if err != nil {
		return err
	}
	switch old := d.(type) {
	case *catalog.Database:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.ForeignObject:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Extension:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Procedure:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.TSDictionary:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Namespace:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		return stage.Replace(&next)
	case *catalog.Collation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		return stage.Replace(&next)
	case *catalog.Conversion:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Operator:
		next := *old
		next.Owner, next.ObjectRevision = target, revision
		return stage.Replace(&next)
	case *catalog.Type:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.Array != 0 {
			array, ok := stage.BaseCatalog().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
			if !ok || !array.SQLType.IsArray() || array.SQLType.Element().ID() != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			if err = stageOwnership(stage, array, target); err != nil {
				return err
			}
		}
		if old.OwningRelation != (ids.ObjectAddress{}) && !old.SQLType.IsArray() {
			r, ok := stage.BaseCatalog().Relation(old.OwningRelation)
			if !ok || r.RowType != old.SQLType.ID() {
				return catalog.Conflict{}
			}
			return stageOwnership(stage, r, target)
		}
	case *catalog.Relation:
		next := *old
		next.Owner = target
		next.ObjectRevision = revision
		next.Security.ACL = authorization.SchemaACLOwner(old.Security.ACL, old.Owner, target)
		if old.Sequence != nil {
			next.SequencePlanStamp = revision
		}
		if len(old.Security.ColumnACL) != 0 {
			next.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(old.Security.ColumnACL))
			for sub, acl := range old.Security.ColumnACL {
				next.Security.ColumnACL[sub] = authorization.SchemaACLOwner(acl, old.Owner, target)
			}
		}
		// Replace synchronizes the row type and its array against this owner.
		if err = stage.Replace(&next); err != nil {
			return err
		}
		if old.RelationKind == catalog.TableRelation || old.RelationKind == catalog.PartitionedRelation || old.RelationKind == catalog.MaterializedViewRelation {
			for _, associated := range stage.BaseCatalog().Descriptors() {
				r, ok := associated.(*catalog.Relation)
				if !ok {
					continue
				}
				linked := r.SQLIndex != nil && r.SQLIndex.Parent == old.Address()
				if r.Sequence != nil {
					parent := r.Sequence.Owned
					parent.SubID = 0
					linked = linked || parent == old.Address()
				}
				if linked {
					if err = stageOwnership(stage, r, target); err != nil {
						return err
					}
				}
			}
		}
	default:
		return fmt.Errorf("stuntgres: object has no ownership handler")
	}
	return nil
}
