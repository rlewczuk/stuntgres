package inquiry

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/sqltype"
)

func IndexProperty(h CatalogHost, kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error) {
	read, _ := h.CommentSnapshot()
	if read.State() == nil {
		return false, false, fmt.Errorf("stuntgres: missing index property catalog snapshot")
	}
	if kind == functions.AccessMethodProperty {
		method, ok := read.AccessMethod(ids.OID(oid))
		if !ok {
			return false, false, nil
		}
		_, indexKind := method.IndexImplementation()
		if !indexKind {
			return false, false, nil
		}
		physical, err := h.InitializeInquiryIndexMethod(read, method)
		if err != nil {
			return false, false, err
		}
		value, known := index.MethodProperty(physical, property)
		return value, known, nil
	}
	relation, ok := read.Relation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	if !ok || relation.SQLIndex == nil {
		return false, false, nil
	}
	ordinal := int64(0)
	if kind != functions.WholeIndexProperty {
		ordinal, err = ordinalValue.Integer()
		if err != nil {
			return false, false, err
		}
		// Released amutils returns NULL for an invalid attribute before
		// invoking the handler, even for an unknown property name.
		if ordinal < 0 || ordinal > int64(len(relation.SQLIndex.Keys)+len(relation.SQLIndex.Include)) {
			return false, false, nil
		}
	}
	method, ok := read.AccessMethod(relation.SQLIndex.MethodIdentity())
	if !ok || method.MethodKind() != "i" {
		return false, false, nil
	}
	physical, err := h.InitializeInquiryIndexMethod(read, method)
	if err != nil {
		return false, false, err
	}
	definition := *relation.SQLIndex
	definition.Method = physical
	value, known = index.Property(read, &definition, kind == functions.WholeIndexProperty, ordinal, property)
	return value, known, nil
}
