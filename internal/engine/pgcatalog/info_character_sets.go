package pgcatalog

import (
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// InfoCharacterSetsOID is the native relation identity, independent of
// PostgreSQL's allocated information-schema relation OID.
const InfoCharacterSetsOID ids.OID = 16206

func captureInfoCharacterSets(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, InfoCharacterSetsOID); err != nil {
		return nil, err
	}
	if in.Owner != in.Catalog.Owner() || len(in.Source.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid character_sets source")
	}
	domain, ok := in.Catalog.LookupType(13248)
	base, baseOK := in.Catalog.LookupType(sqltype.Name)
	if !ok || !baseOK || !domain.IsDomain() || domain.BaseType() != base {
		return nil, fmt.Errorf("stuntgres: invalid character_sets identifier domain")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Type != domain {
			return nil, fmt.Errorf("stuntgres: foreign character_sets identifier domain")
		}
	}
	database, ok := in.Catalog.Database(in.Owner.DatabaseAddress())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing current database descriptor")
	}
	if err := (catalog.DatabaseProvider{}).Validate(in.Catalog, database); err != nil {
		return nil, err
	}

	// Released SQL matches both libc locale fields, with no provider, encoding,
	// schema privilege or search-path filter. LIMIT 1 needs no candidate slice.
	var best *catalog.Collation
	var namespace *catalog.Namespace
	bestLength := 0
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		candidate, ok := descriptor.(*catalog.Collation)
		if !ok || candidate.Collate == nil || candidate.CType == nil || *candidate.Collate != nativeDatabaseLocale || *candidate.CType != nativeDatabaseLocale {
			continue
		}
		ns, ok := in.Catalog.Namespace(candidate.Namespace())
		if !ok {
			continue
		}
		length := utf8.RuneCountInString(candidate.Name())
		if best == nil || length > bestLength || length == bestLength && candidate.Name() < best.Name() {
			best, namespace, bestLength = candidate, ns, length
		}
	}
	row := make([]sqltype.Value, 8)
	row[0], row[1] = sqltype.Null(domain), sqltype.Null(domain)
	values := [6]string{nativeDatabaseEncodingName, "UCS", nativeDatabaseEncodingName, in.Runtime.DatabaseName()}
	if best != nil {
		values[4], values[5] = namespace.Name(), best.Name()
	}
	for i, text := range values {
		if i >= 4 && best == nil {
			row[i+2] = sqltype.Null(domain)
			continue
		}
		value, err := domain.Wrap(sqltype.String(base, text))
		if err != nil {
			return nil, err
		}
		row[i+2] = value
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}
