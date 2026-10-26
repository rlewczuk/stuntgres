package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoCollationsOID is the native relation identity, independent of
// PostgreSQL's allocated information-schema relation OID.
const InfoCollationsOID ids.OID = 16229

func captureInfoCollations(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoCollationsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid collations source")
	}
	identifier, identifierOK := in.Catalog.LookupType(13248)
	name, nameOK := in.Catalog.LookupType(sqltype.Name)
	characterData, characterDataOK := in.Catalog.LookupType(13246)
	varchar, varcharOK := in.Catalog.LookupType(sqltype.VarChar)
	if !identifierOK || !nameOK || !identifier.IsDomain() || identifier.BaseType() != name || !characterDataOK || !varcharOK || !characterData.IsDomain() || characterData.BaseType() != varchar {
		return nil, fmt.Errorf("stuntgres: invalid collations domains")
	}
	for i, column := range in.Source.Columns {
		domain := identifier
		if i == 3 {
			domain = characterData
		}
		if column.Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: foreign collations domain")
		}
	}
	database, ok := in.Catalog.Database(in.Owner.DatabaseAddress())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	if err := (catalog.DatabaseProvider{}).Validate(in.Catalog, database); err != nil {
		return nil, err
	}
	databaseName, err := identifier.Wrap(sqltype.String(name, in.Runtime.DatabaseName()))
	if err != nil {
		return nil, err
	}
	pad, err := characterData.Wrap(sqltype.String(varchar, "NO PAD"))
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		collation, ok := descriptor.(*catalog.Collation)
		if !ok || collation.Encoding != -1 && collation.Encoding != nativeDatabaseEncodingID {
			continue
		}
		namespace, ok := in.Catalog.Namespace(collation.Namespace())
		if !ok {
			continue
		}
		// Catalog inquiry includes real inquiry-only descriptors and does not
		// require schema USAGE, search-path visibility or provider execution.
		schema, err := identifier.Wrap(sqltype.String(name, namespace.Name()))
		if err != nil {
			return nil, err
		}
		collationName, err := identifier.Wrap(sqltype.String(name, collation.Name()))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{databaseName, schema, collationName, pad})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
