package index

import (
	"context"
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

// GinPresentationKeyUpdate is an unpublished replacement certified against a
// concrete stage predecessor. The owning core allocates its revision under Mu;
// provider validation here runs before that lock, never in the apply phase.
type GinPresentationKeyUpdate struct{ Source, Target *catalog.Relation }

func CaptureGinPresentationKeys(ctx context.Context, before, after catalog.Read) ([]GinPresentationKeyUpdate, error) {
	var updates []GinPresentationKeyUpdate
	for _, source := range before.Relations() {
		if source.SQLIndex == nil || source.SQLIndex.Method != catalog.GinMethod {
			continue
		}
		target, ok := after.Relation(source.Address())
		if !ok || !ginPresentationIndexSemantics(source, target) {
			continue
		}
		var next *catalog.Relation
		for column, key := range source.SQLIndex.Keys {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !ginPresentationChanged(before, after, key.Class.OID(), key.Class.FamilyOID(), key.Class.InputOID()) {
				continue
			}
			current, err := ginPresentationClass(before, after, key.Class)
			if err != nil {
				return nil, err
			}
			if current == target.SQLIndex.Keys[column].Class {
				continue
			}
			if next == nil {
				copy, definition := *target, *target.SQLIndex
				definition.Keys = slices.Clone(definition.Keys)
				copy.SQLIndex = &definition
				next = &copy
			}
			next.SQLIndex.Keys[column].Class = current
		}
		if next != nil {
			updates = append(updates, GinPresentationKeyUpdate{Source: target, Target: next})
		}
	}
	return updates, ctx.Err()
}
func ginPresentationChanged(before, after catalog.Read, class, family, input uint32) bool {
	for _, a := range [...]ids.ObjectAddress{{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(class)}, {ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(family)}} {
		if before.Get(a) != after.Get(a) {
			return true
		}
	}
	for slot := int16(1); slot <= 6; slot++ {
		member, ok := before.FamilySupport(ids.OID(family), ids.OID(input), ids.OID(input), slot)
		if ok {
			a := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function}
			if before.Get(a) != after.Get(a) {
				return true
			}
		}
	}
	return false
}

// Pointer changes remain forbidden for builtins and foreign providers. Genuine
// installed scalar helpers may change only their SQL presentation/membership;
// both current declarations have already passed strict provider validation in
// ginPresentationClass. The original sealed recipe must be the very same one.
func ginPresentationProcedure(before, after catalog.Read, address ids.ObjectAddress) bool {
	a, b := before.Get(address), after.Get(address)
	if a == b {
		return true
	}
	old, ok := a.(*catalog.Procedure)
	if !ok {
		return false
	}
	current, ok := b.(*catalog.Procedure)
	if !ok {
		return false
	}
	if old.Implementation != extension.StockBtreeGin || old.Origin == nil || current.Origin != old.Origin || !exec.SameRoutineDeclaration(current, old) {
		return false
	}
	// OWNER rewrites grantor/grantee references, including coalesced grants.
	// No other ACL difference is executable presentation authority.
	expectedACL := old.ACL
	if old.Owner != current.Owner {
		expectedACL = authorization.SchemaACLOwner(old.ACL, old.Owner, current.Owner)
	}
	if !authorization.EqualACL(current.ACL, expectedACL) {
		return false
	}
	return catalog.ValidateExtensionMembership(before, old) == nil && catalog.ValidateExtensionMembership(after, current) == nil
}
