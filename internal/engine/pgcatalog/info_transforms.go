package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoTransformsOID is the native identity, independent of PostgreSQL allocation.
const InfoTransformsOID ids.OID = 16242

var infoTransformsColumns = []ColumnSpec{
	{"udt_catalog", 13248, true, 950, 0, -1},
	{"udt_schema", 13248, true, 950, 0, -1},
	{"udt_name", 13248, true, 950, 0, -1},
	{"specific_catalog", 13248, true, 950, 0, -1},
	{"specific_schema", 13248, true, 950, 0, -1},
	{"specific_name", 13248, true, 950, 0, -1},
	{"group_name", 13248, true, 950, 0, -1},
	{"transform_type", 13246, true, 950, 0, -1},
}

func captureInfoTransforms(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: missing transforms runtime")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoTransformsOID); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Owner != in.Catalog.Owner() || in.Source == nil || in.Source.Name() != "transforms" || in.Source.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || !in.Source.VirtualView || len(in.Source.Columns) != len(infoTransformsColumns) || len(in.Source.Attributes) != len(infoTransformsColumns) {
		return nil, fmt.Errorf("stuntgres: invalid transforms source")
	}
	for i, spec := range infoTransformsColumns {
		column := in.Source.Columns[i]
		domain, ok := in.Catalog.LookupType(spec.Type)
		baseID := sqltype.Name
		if i == 7 {
			baseID = sqltype.VarChar
		}
		base, baseOK := in.Catalog.LookupType(baseID)
		if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base || column.Name != spec.Name || column.Usage != domain.Usage() || !column.Nullable || !column.NullabilityKnown || column.DeclaredDimensions != 0 || column.OriginTable != uint32(InfoTransformsOID) || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			return nil, fmt.Errorf("stuntgres: invalid transforms domain authority")
		}
	}
	// As with pg_transform, no implemented objects own transform definitions.
	// Ordinary types, languages, codecs and SQL routines are not transforms.
	return [][]sqltype.Value{}, nil
}
