package catalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IndexRoutineCallback owns one actually selected role and its complete source
// generation. It retains immutable declarations, never a runtime borrower.
type IndexRoutineCallback struct {
	Support           sqltype.IndexSupport
	Slot              uint16
	Class             *OperatorClass
	Family            *OperatorFamily
	Member            *OperatorFamilySupport
	Helper            *Procedure
	Reference, Source ids.ObjectRef
	classStamp        OperatorClass
	familyStamp       OperatorFamily
	memberStamp       OperatorFamilySupport
	routineStamp      orderingRoutineStamp
}

func CaptureIndexRoutineCallback(view Read, support sqltype.IndexSupport, slot uint16) (IndexRoutineCallback, error) {
	if view.Owner() == nil || slot == 0 || slot > 6 || support.Class == 0 {
		return IndexRoutineCallback{}, fmt.Errorf("stuntgres: missing selected index support")
	}
	class, ok := view.OperatorClass(ids.OID(support.Class))
	if !ok || class.Revision() != ids.DefinitionRevision(support.ClassRevision) || class.Family != ids.OID(support.Family) {
		return IndexRoutineCallback{}, ids.ErrCatalogReplaced
	}
	family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: class.Family})
	if !ok || family.Method != class.Method || family.Revision() != ids.DefinitionRevision(support.FamilyRevision) {
		return IndexRoutineCallback{}, ids.ErrCatalogReplaced
	}
	member, ok := view.FamilySupport(class.Family, ids.OID(support.Left), ids.OID(support.Right), int16(slot))
	if !ok || member.Address().ObjectID != ids.OID(support.Member) || member.Function != ids.OID(support.Routine) || member.Revision() != ids.DefinitionRevision(support.Revision) {
		return IndexRoutineCallback{}, ids.ErrCatalogReplaced
	}
	// Cross-type foreign-key comparisons select a real member of the captured
	// class's family; that member may be owned by another class in the family.
	if member.InternalOwner != (ids.ObjectAddress{}) && member.InternalOwner != class.Address() {
		owner, ok := view.OperatorClass(member.InternalOwner.ObjectID)
		if !ok || owner.Address() != member.InternalOwner || owner.Family != class.Family || owner.Method != class.Method {
			return IndexRoutineCallback{}, ids.ErrCatalogReplaced
		}
	}
	helper, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
	if !ok || helper.Revision() != ids.DefinitionRevision(support.RoutineRevision) {
		return IndexRoutineCallback{}, ids.ErrCatalogReplaced
	}
	if support.Authority != nil && (class.Origin == nil || class.Origin.Installation.IndexAuthority() != support.Authority) {
		return IndexRoutineCallback{}, ids.ErrCatalogReplaced
	}
	for _, descriptor := range [4]Descriptor{class, family, member, helper} {
		provider := view.Owner().Provider(descriptor.Address().ClassID)
		if provider == nil {
			return IndexRoutineCallback{}, ids.ErrCatalogReplaced
		}
		if err := provider.Validate(view, descriptor); err != nil {
			return IndexRoutineCallback{}, err
		}
	}
	return IndexRoutineCallback{
		Support: support, Slot: slot, Class: class, Family: family, Member: member, Helper: helper,
		Reference: view.Domain().Ref(view.Epoch(), helper.Address()), Source: view.Domain().Ref(view.Epoch(), member.Address()),
		classStamp: *class, familyStamp: *family, memberStamp: *member, routineStamp: orderingRoutineOf(helper),
	}, nil
}

func (c IndexRoutineCallback) Validate(view Read) error {
	if c.Reference.Domain() != view.Domain() || c.Reference.Epoch != view.Epoch() || c.Source.Domain() != view.Domain() || c.Source.Epoch != view.Epoch() || c.Class == nil || c.Family == nil || c.Member == nil || c.Helper == nil ||
		*c.Class != c.classStamp || *c.Family != c.familyStamp || *c.Member != c.memberStamp || orderingRoutineOf(c.Helper) != c.routineStamp {
		return ids.ErrCatalogReplaced
	}
	current, err := CaptureIndexRoutineCallback(view, c.Support, c.Slot)
	if err != nil {
		return err
	}
	if current.Reference != c.Reference || current.Source != c.Source || current.classStamp != c.classStamp || current.familyStamp != c.familyStamp || current.memberStamp != c.memberStamp || current.routineStamp != c.routineStamp {
		return ids.ErrCatalogReplaced
	}
	return nil
}
