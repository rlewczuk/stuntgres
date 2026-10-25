package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
)

// TypeUsageAuthority follows PostgreSQL's array-then-multirange delegation.
// Domains retain their own ACL. Every hop uses current reciprocal identities;
// no name, copied ACL, or merely equal OID supplies authorization authority.
func TypeUsageAuthority(view Read, typ *Type) (*Type, error) {
	if typ == nil || typ.SQLType == nil {
		return nil, fmt.Errorf("missing type usage authority")
	}
	actual, ok := view.Type(typ.Address())
	if !ok || actual != typ {
		return nil, fmt.Errorf("stale type usage authority")
	}
	if typ.SQLType.IsArray() && !typ.SQLType.IsCatalogVector() {
		var err error
		typ, err = ArrayTypeOwner(view, typ)
		if err != nil {
			return nil, err
		}
	}
	if typ.SQLType.IsMultirange() {
		rangeType := typ.SQLType.RangeType()
		owner, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(rangeType.ID())})
		if !ok || owner.SQLType != rangeType || rangeType.MultirangeType() != typ.SQLType || owner.Owner != typ.Owner {
			return nil, fmt.Errorf("invalid multirange usage authority")
		}
		typ = owner
	}
	return typ, nil
}
