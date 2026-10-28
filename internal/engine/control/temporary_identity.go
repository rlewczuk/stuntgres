package control

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// TemporaryCollisionMapping preserves live TEMP identities. Only genuinely
// affected alias endpoint components require structural slot planning; all
// other imports retain ordinary selective collision mapping.
// The caller holds Engine.mu; allocation never evaluates subtype providers.
func TemporaryCollisionMapping(ctx context.Context, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor, constraints []catalog.AliasOrderConstraint, allocate func(ids.OID) (ids.OID, error)) (map[ids.ObjectAddress]ids.ObjectAddress, error) {
	if len(temporary) == 0 {
		return nil, ctx.Err()
	}
	retained := make(map[ids.OID]bool)
	for address, d := range temporary {
		retained[address.ObjectID] = true
		catalog.ReserveDefinitionOIDs(retained, d)
	}
	reserved := make(map[ids.OID]bool, next.DescriptorCount()+len(retained))
	for id := range retained {
		reserved[id] = true
	}
	for address, d := range next.Descriptors() {
		reserved[address.ObjectID] = true
		catalog.ReserveDefinitionOIDs(reserved, d)
	}
	mapping, err := planAliasIdentities(ctx, next, retained, reserved, constraints, allocate)
	if err != nil {
		return nil, err
	}
	if mapping != nil {
		// A constrained plan already assigns every ordinary collision from its
		// owned slot pool; never consume another identity after that solution.
		return mapping, ctx.Err()
	}
	remint := func(address ids.ObjectAddress) error {
		if !retained[address.ObjectID] || mapping[address] != (ids.ObjectAddress{}) {
			return nil
		}
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			id, err := allocate(0)
			if err != nil {
				return err
			}
			if reserved[id] {
				continue
			}
			reserved[id] = true
			if mapping == nil {
				mapping = make(map[ids.ObjectAddress]ids.ObjectAddress)
			}
			target := address
			target.ObjectID = id
			mapping[address] = target
			return nil
		}
	}
	if err := visitTemporaryCollisions(ctx, next, retained, remint); err != nil {
		return nil, err
	}
	return mapping, ctx.Err()
}

// Both selective reminting and constrained complete planning use the same
// descriptor/helper eligibility. The unaffected fastpath allocates no demand
// list or deduplication map; its result map already suppresses duplicate visits.
func visitTemporaryCollisions(ctx context.Context, next *catalog.State, retained map[ids.OID]bool, visit func(ids.ObjectAddress) error) error {
	for address, descriptor := range next.Descriptors() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if address.ClassID == ids.LargeObjectClassID {
			continue
		}
		if retained[address.ObjectID] {
			if err := visit(address); err != nil {
				return err
			}
		}
		if procedure, ok := descriptor.(*catalog.Procedure); ok && procedure.Aggregate != nil && procedure.Implementation != nil {
			for _, helper := range procedure.Aggregate.Helpers {
				if retained[helper.ID] {
					if err := visit(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}); err != nil {
						return err
					}
				}
			}
		}
	}
	return ctx.Err()
}

func movableImportedIdentity(next *catalog.State, d catalog.Descriptor) bool {
	if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) {
		return true
	}
	switch t := d.(type) {
	case *catalog.Type:
		physical := t.SQLType
		for physical.IsArray() {
			physical = physical.Element()
		}
		if physical.IsBuiltin() {
			return false
		}
		if element, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(physical.ID())}); ok && element.OwningRelation != (ids.ObjectAddress{}) {
			if relation, ok := next.Relation(element.OwningRelation); ok && relation.RelationKind == catalog.CatalogRelation {
				return false
			}
		}
		return true
	case *catalog.Namespace:
		return t.Address().ObjectID != ids.PGCatalogNamespaceOID && t.Address().ObjectID != ids.PGToastNamespaceOID && t.Address().ObjectID != ids.PublicNamespaceOID && t.Address().ObjectID != 13229
	case *catalog.Relation:
		return t.RelationKind != catalog.CatalogRelation
	case *catalog.Procedure:
		_, builtin := catalog.BuiltinRoutineIdentityByOID(t.Address().ObjectID)
		return !builtin && !t.Seeded()
	case *catalog.Collation:
		return !t.Seeded()
	case *catalog.Statistics:
		return true
	case *catalog.Conversion:
		return !t.Seeded()
	case *catalog.TSParser:
		return !t.Seeded()
	case *catalog.TSConfiguration:
		return !t.Seeded()
	case *catalog.TSDictionary:
		return !t.Seeded()
	case *catalog.TSTemplate:
		return !t.Seeded()
	case *catalog.AccessMethod:
		return !catalog.AccessMethodSeeded(t)
	case *catalog.Operator:
		return !routine.OperatorSeeded(t)
	case *catalog.OperatorClass:
		return !catalog.OperatorClassSeeded(t)
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return !catalog.OperatorFamilySeeded(t)
	case *catalog.Subscription, *catalog.EventTrigger, *catalog.Cast, *catalog.Extension, *catalog.ForeignObject:
		return true
	default:
		return d.Namespace() != (ids.ObjectAddress{}) && d.Namespace().ObjectID != ids.PGCatalogNamespaceOID
	}
}
