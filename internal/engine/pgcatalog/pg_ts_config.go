package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const TSConfigOID ids.OID = 3602
const TSConfigMapOID ids.OID = 3603

func capturePGTSConfig(in Input) ([][]sqltype.Value, error) { return captureTSConfiguration(in, false) }
func capturePGTSConfigMap(in Input) ([][]sqltype.Value, error) {
	return captureTSConfiguration(in, true)
}
func captureTSConfiguration(in Input, mappings bool) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	shape := []sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Oid}
	if mappings {
		shape = []sqltype.ID{sqltype.Oid, sqltype.Int4, sqltype.Int4, sqltype.Oid}
	}
	if len(in.Source.Columns) != len(shape) {
		return nil, fmt.Errorf("stuntgres: invalid text search configuration source shape")
	}
	for i, id := range shape {
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != in.Source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign text search configuration source type")
		}
	}
	rows := [][]sqltype.Value{}
	for _, raw := range in.Catalog.Descriptors() {
		d, ok := raw.(*catalog.TSConfiguration)
		if !ok {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.TSConfigurationProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		add := func(numbers []int64, name string) error {
			row := make([]sqltype.Value, len(shape))
			for i, id := range shape {
				typ := in.Source.Columns[i].Usage.Type
				if id == sqltype.Name {
					row[i] = sqltype.String(typ, name)
					continue
				}
				var value sqltype.Value
				var err error
				if id == sqltype.Oid {
					value, err = sqltype.OIDValue(typ, uint32(numbers[i]))
				} else {
					value, err = sqltype.Integer(typ, numbers[i])
				}
				if err != nil {
					return err
				}
				row[i] = value
			}
			rows = append(rows, row)
			return nil
		}
		if !mappings {
			if err := add([]int64{int64(d.Address().ObjectID), 0, int64(d.Namespace().ObjectID), int64(d.Owner.ID()), int64(d.Parser.ObjectID)}, d.Name()); err != nil {
				return nil, err
			}
			continue
		}
		for _, m := range d.Mappings {
			for i, a := range m.Dictionaries {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				if err := add([]int64{int64(d.Address().ObjectID), int64(m.TokenID), int64(i + 1), int64(a.ObjectID)}, ""); err != nil {
					return nil, err
				}
			}
		}
	}
	return rows, nil
}
