package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGCast(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 6 {
		return nil, fmt.Errorf("stuntgres: invalid cast source shape")
	}
	for i, column := range source.Columns {
		id := sqltype.Oid
		if i >= 4 {
			id = sqltype.Char
		}
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign cast source type")
		}
	}
	count := sqltype.CastMetadataCount()
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*6)
	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fact, ok := sqltype.CastMetadataAt(i)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing cast owner metadata")
		}
		from, sourceOK := catalog.LookupType(fact.Source)
		to, targetOK := catalog.LookupType(fact.Target)
		if !sourceOK || !targetOK || from.ID() != fact.Source || to.ID() != fact.Target {
			return nil, fmt.Errorf("stuntgres: missing cast source or target type")
		}
		row := values[i*6 : (i+1)*6 : (i+1)*6]
		for j, oid := range [4]uint32{fact.OID, uint32(fact.Source), uint32(fact.Target), fact.Function} {
			value, err := sqltype.OIDValue(source.Columns[j].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		context, err := castCatalogCharacter(fact.Context)
		if err != nil {
			return nil, err
		}
		method, err := castCatalogCharacter(fact.Method)
		if err != nil {
			return nil, err
		}
		row[4] = sqltype.String(source.Columns[4].Usage.Type, context)
		row[5] = sqltype.String(source.Columns[5].Usage.Type, method)
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Return static strings rather than allocate a string for each projected byte.
func castCatalogCharacter(value byte) (string, error) {
	switch value {
	case 'i':
		return "i", nil
	case 'a':
		return "a", nil
	case 'e':
		return "e", nil
	case 'f':
		return "f", nil
	case 'b':
		return "b", nil
	default:
		return "", fmt.Errorf("stuntgres: invalid cast catalog character")
	}
}
