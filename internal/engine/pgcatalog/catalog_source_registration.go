package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RegisteredSource reports whether source is the captured catalog's descriptor
// for a registered system relation. ACLs and revisions may change, but source
// identity, relkind and stable attributes must retain their registration facts.
// Registration admits ACL planning, not writes or access to backing sources.
func RegisteredSource(view catalog.Read, source *catalog.Relation) bool {
	if source == nil || view.Owner() == nil || view.State() == nil || source.RelationKind != catalog.CatalogRelation || view.Get(source.Address()) != source || source.Revision() == 0 || view.NameAddress(catalog.DescriptorNameKey(source)) != source.Address() {
		return false
	}
	for i := range Specs {
		spec := &Specs[i]
		if source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: spec.OID}) {
			continue
		}
		if source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: spec.Namespace}) || source.Name() != spec.Name || source.VirtualView != spec.Virtual || source.CatalogView != (spec.Virtual || SummaryViewDefinition(spec.OID) != "") || len(source.Columns) != len(spec.Columns) || len(source.Attributes) != len(spec.Columns) || len(source.Index) != len(spec.Columns) {
			return false
		}
		for j, column := range spec.Columns {
			typ, ok := view.LookupType(column.Type)
			attribute := AttributeNumber(spec.OID, column.Name, int32(j+1))
			index, indexed := source.Index[column.Name]
			actual := source.Columns[j]
			if !ok || actual.Usage.Type != typ || actual.Name != column.Name || source.Attributes[j] != attribute || actual.OriginTable != uint32(spec.OID) || actual.OriginAttribute != int16(attribute) || !indexed || index != j {
				return false
			}
		}
		return true
	}
	return false
}
