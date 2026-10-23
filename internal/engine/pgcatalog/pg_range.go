package pgcatalog

import (
	"fmt"
	catalogtypes "stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const pgRangeOID ids.OID = 3541

func capturePGRange(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	catalog := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || len(source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid range source shape")
	}
	for i, column := range source.Columns {
		id := sqltype.Oid
		if i >= 5 {
			id = sqltype.Regproc
		}
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign range source type")
		}
	}
	var facts []sqltype.RangeMetadata
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, ok := descriptor.(*catalogtypes.Type)
		if !ok || !t.SQLType.IsRange() {
			continue
		}
		fact, ok := t.SQLType.RangeMetadata()
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid range owner metadata")
		}
		facts = append(facts, fact)
	}
	rows := make([][]sqltype.Value, len(facts))
	values := make([]sqltype.Value, len(facts)*7)
	for i, fact := range facts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*7 : (i+1)*7 : (i+1)*7]
		for j, oid := range [...]uint32{fact.Type, fact.Subtype, fact.Multitype, fact.Collation, fact.Subclass, fact.Canonical, fact.Subdiff} {
			var value sqltype.Value
			var err error
			if j < 5 {
				value, err = sqltype.OIDValue(source.Columns[j].Usage.Type, oid)
			} else {
				value, err = sqltype.RegprocValue(source.Columns[j].Usage.Type, oid)
			}
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		rows[i] = row
	}
	return rows, nil
}
