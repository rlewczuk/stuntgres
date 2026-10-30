package storage

import (
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqltype"
)

// AttributeStatisticsHasLessThan checks captured operator metadata, rather than
// admission to the narrower native ORDER BY/index execution implementation.
func AttributeStatisticsHasLessThan(read catalog.Read, usage sqltype.Usage) (bool, error) {
	if usage.Type == nil {
		return false, nil
	}
	class, found, err := read.DefaultIndexClass(403, usage.Type)
	if err != nil {
		return false, err
	}
	if found {
		member, found := read.FamilyOperator(ids.OID(class.FamilyOID()), ids.OID(class.InputOID()), ids.OID(class.InputOID()), 1, 's')
		if !found {
			return false, nil
		}
		_, found = read.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: member.Operator})
		return found, nil
	}

	// Generic comparison operators are catalogued even when their btree class
	// is deliberately not advertised as a native index execution provider.
	// Resolve their declared polymorphic signature, not a concrete array OID.
	base := usage.Type.BaseType()
	var signature string
	switch {
	case base.IsArray():
		available, err := AttributeStatisticsHasLessThan(read, base.Element().Usage())
		if err != nil || !available {
			return false, err
		}
		signature = "2277,2277"
	case base.IsRange():
		signature = "3831,3831"
	case base.IsMultirange():
		signature = "4537,4537"
	case base.IsComposite():
		for i := 0; i < base.CompositeLen(); i++ {
			available, err := AttributeStatisticsHasLessThan(read, base.CompositeMember(i).Usage)
			if err != nil || !available {
				return false, err
			}
		}
		signature = "2249,2249"
	default:
		return false, nil
	}
	operator, found := read.Operator(read.NameAddress(ids.NameKey{
		ClassID: ids.OperatorClassID,
		Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
		Name: "<",
		Signature: signature,
	}))
	return found && operator.Result == ids.OID(sqltype.Bool) && operator.Function != 0, nil
}

// AttributeStatisticsElementType supplies the shared SQL/native eligibility
// check for both element MCVs and the numbers-only element-count histogram.
func AttributeStatisticsElementType(read catalog.Read, typ *sqltype.Descriptor) (*sqltype.Descriptor, bool) {
	var element *sqltype.Descriptor
	if typ != nil {
		if typ.ID() == sqltype.TSVector {
			element, _ = read.LookupType(sqltype.Text)
		} else if typ.BaseType().IsArray() {
			element = typ.BaseType().Element()
		}
	}
	if element == nil {
		return nil, false
	}
	if _, err := sqltype.BindGrouping(element.Usage()); err != nil {
		return nil, false
	}
	return element, true
}
