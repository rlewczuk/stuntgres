package typedef

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StageMoveTypeNamespace is the direct type member entry, not public ALTER
// TYPE: ownership precedes ignoring generated arrays/relation row types. The
// shared identity owner performs internal companion moves without new gates.
func StageMoveTypeNamespace(h StageHost, auth ddl.Authority, old *catalog.Type, destination ids.ObjectAddress, moved map[ids.ObjectAddress]bool) (ids.ObjectAddress, error) {
	current := h.StageRead()
	auth = h.Recapture(auth, current)
	if err := h.AccessView(auth).Check(auth, access.Requirement{Object: ddl.ObjectRef(h, auth, old.Address()), OwnerOnly: true}); err != nil {
		return ids.ObjectAddress{}, err
	}
	if old.SQLType.IsArray() || old.OwningRelation != (ids.ObjectAddress{}) && current.MustRelation(old.OwningRelation).RelationKind != catalog.CompositeRelation {
		return ids.ObjectAddress{}, nil
	}
	if moved[old.Address()] {
		return ids.ObjectAddress{}, nil
	}
	previous := old.Namespace()
	if previous == destination {
		return previous, nil
	}
	h.AddDomainBase(old)
	if err := catalog.CheckSetNamespace(current, previous, destination); err != nil {
		return ids.ObjectAddress{}, err
	}
	path, err := auth.ReferencePath()
	if err != nil {
		return ids.ObjectAddress{}, err
	}
	if err := stageTypeIdentityReplacement(h, old, destination, old.Name(), true, old.SQLType.IsDomain(), path); err != nil {
		return ids.ObjectAddress{}, err
	}
	moved[old.Address()] = true
	if old.Array != 0 {
		moved[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)}] = true
	}
	return previous, nil
}

// Refresh reciprocal range generations without moving an unrelated multirange or
// changing generated constructor public identities.
func stageRangeIdentity(h StageHost, old *catalog.Type, destination ids.ObjectAddress, name string, moving bool) error {
	view := h.StageRead()
	rangeType := old.SQLType.RangeType()
	if rangeType == nil {
		return fmt.Errorf("stuntgres: missing range identity")
	}
	r := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rangeType.ID())})
	m := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rangeType.MultirangeType().ID())})
	definition, ok := r.SQLType.RangeDefinition()
	if !ok {
		return fmt.Errorf("stuntgres: builtin range identity is immutable")
	}
	rn, mn := *r, *m
	rName, mName := r.Name(), m.Name()
	if old.SQLType.IsRange() {
		rn.ObjectNamespace = destination
		rName = name
	} else {
		mn.ObjectNamespace = destination
		mName = name
	}
	qualified := func(ns ids.ObjectAddress, n string) string {
		return catalog.DependencyIdentifier(view.MustNamespace(ns).Name()) + "." + catalog.DependencyIdentifier(n)
	}
	boolean, _ := view.LookupType(sqltype.Bool)
	var err error
	rn.SQLType, mn.SQLType, err = sqltype.NewRangeFamily(r.SQLType.ID(), rName, qualified(rn.Namespace(), rName), m.SQLType.ID(), mName, qualified(mn.Namespace(), mName), definition, boolean)
	if err != nil {
		return err
	}
	for _, t := range []*catalog.Type{&rn, &mn} {
		if t.Namespace() != view.MustType(t.Address()).Namespace() || t.Name() != view.MustType(t.Address()).Name() {
			if a := h.Stage().LookupName(catalog.DescriptorNameKey(t)); a != (ids.ObjectAddress{}) && a != t.Address() {
				return sqlError("42710", fmt.Sprintf("type %q already exists in schema %q", t.Name(), view.MustNamespace(t.Namespace()).Name()))
			}
		}
		t.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		if err := h.ReplaceUnsynchronized(t); err != nil {
			return err
		}
		array := *view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(t.Array)})
		array.ObjectNamespace = t.Namespace()
		arrayName := array.Name()
		if !moving && t.Address() == old.Address() {
			arrayName = h.AssociatedArrayName(t.Namespace(), t.Name())
		}
		array.SQLType, err = sqltype.Array(array.SQLType.ID(), arrayName, t.SQLType)
		if err != nil {
			return err
		}
		if a := h.Stage().LookupName(catalog.DescriptorNameKey(&array)); a != (ids.ObjectAddress{}) && a != array.Address() {
			return sqlError("42710", fmt.Sprintf("type %q already exists in schema %q", array.Name(), view.MustNamespace(array.Namespace()).Name()))
		}
		array.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		if err := h.ReplaceUnsynchronized(&array); err != nil {
			return err
		}
	}
	for _, d := range view.Descriptors() {
		p, ok := d.(*catalog.Procedure)
		if !ok || p.RangeConstructor == nil {
			continue
		}
		var typ *sqltype.Descriptor
		switch p.RangeConstructor.Type.ID() {
		case rn.SQLType.ID():
			typ = rn.SQLType
		case mn.SQLType.ID():
			typ = mn.SQLType
		default:
			continue
		}
		next, provider := *p, *p.RangeConstructor
		provider.Type = typ
		next.RangeConstructor = &provider
		next.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		if err := h.Stage().Replace(&next); err != nil {
			return err
		}
	}
	return nil
}
