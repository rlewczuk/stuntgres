package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
)

// Pinning follows represented bootstrap authorities, never an OID cutoff.
func DependPinned(view catalog.Read, reader catalog.DefinitionReader, address ids.ObjectAddress) bool {
	if address.ClassID == ids.NamespaceClassID {
		return address.ObjectID == ids.PGCatalogNamespaceOID || address.ObjectID == ids.PGToastNamespaceOID
	}
	whole := address
	whole.SubID = 0
	d, ok := catalog.DescriptorAt(view, reader, whole)
	if !ok {
		return false
	}
	switch d := d.(type) {
	case *catalog.AccessMethod:
		return catalog.AccessMethodSeeded(d)
	case *catalog.Type:
		return catalog.TypePinned(view, reader, d)
	case *catalog.Collation:
		return d.Seeded()
	case *catalog.Conversion:
		return d.Seeded()
	case *catalog.TSDictionary:
		return d.Seeded()
	case *catalog.TSTemplate:
		return d.Seeded()
	case *catalog.Procedure:
		return d.Seeded()
	case *catalog.OperatorClass:
		return catalog.OperatorClassSeeded(d)
	case *catalog.OperatorFamily, *catalog.OperatorFamilyOperator, *catalog.OperatorFamilySupport:
		return catalog.OperatorFamilySeeded(d)
	case *catalog.Operator:
		return routine.OperatorSeeded(d)
	case *catalog.Relation:
		return d.RelationKind == catalog.CatalogRelation && pgcatalog.ClassKind(view, d) == "r"
	}
	return false
}
