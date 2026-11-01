package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoInformationSchemaCatalogNameOID identifies the native relation, not its
// canonical composite type (13251) or companion array (13250).
const InfoInformationSchemaCatalogNameOID ids.OID = 16251

func captureInfoInformationSchemaCatalogName(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoInformationSchemaCatalogNameOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 1 {
		return nil, fmt.Errorf("stuntgres: invalid information_schema_catalog_name source")
	}
	identifier, identifierOK := in.Catalog.LookupType(13248)
	name, nameOK := in.Catalog.LookupType(sqltype.Name)
	column := in.Source.Columns[0]
	if !identifierOK || !nameOK || !identifier.IsDomain() || identifier.DomainBase().Type != name || column.Name != "catalog_name" || column.Usage != identifier.Usage() || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 {
		return nil, fmt.Errorf("stuntgres: invalid information_schema_catalog_name identifier domain")
	}
	database, ok := in.Catalog.Database(in.Owner.DatabaseAddress())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	if err := (catalog.DatabaseProvider{}).Validate(in.Catalog, database); err != nil {
		return nil, err
	}
	// The inquiry owns this scalar, including a legitimate internal empty name;
	// descriptor spelling and live session state are not substitutes for it.
	value, err := identifier.Wrap(sqltype.String(name, in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	return [][]sqltype.Value{{value}}, nil
}
