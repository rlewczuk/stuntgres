package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

const pgRangeOID OID = 3541

func capturePGRange(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || len(source.columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid range source shape")
	}
	for i, column := range source.columns {
		id := sqltype.Oid
		if i >= 5 {
			id = sqltype.Regproc
		}
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign range source type")
		}
	}
	var facts [6]sqltype.RangeMetadata
	count := 0
	for _, id := range [...]sqltype.ID{sqltype.Int4Range, sqltype.NumRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.DateRange, sqltype.Int8Range} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := catalog.LookupType(id)
		if !ok || !d.Concrete() {
			continue
		}
		fact, ok := d.RangeMetadata()
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid range owner metadata")
		}
		facts[count] = fact
		count++
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*7)
	for i, fact := range facts[:count] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*7 : (i+1)*7 : (i+1)*7]
		for j, oid := range [...]uint32{fact.Type, fact.Subtype, fact.Multitype, fact.Collation, fact.Subclass, fact.Canonical, fact.Subdiff} {
			var value sqltype.Value
			var err error
			if j < 5 {
				value, err = sqltype.OIDValue(source.columns[j].Usage.Type, oid)
			} else {
				value, err = sqltype.RegprocValue(source.columns[j].Usage.Type, oid)
			}
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
