package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture immutable definitions from the caller's coherent catalog generation;
// catalog labels are metadata, not enum datum input (including unsafe members).
func capturePGEnum(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid enum source shape")
	}
	for i, id := range [4]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Float4, sqltype.Name} {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign enum source type")
		}
	}
	count := 0
	for address, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Type)
		if !ok || !d.SQLType.IsEnum() {
			continue
		}
		if address != d.Address() || address.ClassID != ids.TypeClassID || address.ObjectID == 0 || sqltype.ID(address.ObjectID) != d.SQLType.ID() {
			return nil, fmt.Errorf("stuntgres: invalid enum owner identity")
		}
		count += d.SQLType.EnumMemberCount()
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*4)
	at := 0
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Type)
		if !ok || !d.SQLType.IsEnum() {
			continue
		}
		for i := range d.SQLType.EnumMemberCount() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			member, ok := d.SQLType.EnumMemberAt(i)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing enum member metadata")
			}
			row := values[at*4 : (at+1)*4 : (at+1)*4]
			var err error
			row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(member.ID))
			if err != nil {
				return nil, err
			}
			row[1], err = sqltype.OIDValue(source.Columns[1].Usage.Type, uint32(d.SQLType.ID()))
			if err != nil {
				return nil, err
			}
			row[2], err = sqltype.Float(source.Columns[2].Usage.Type, float64(member.SortOrder))
			if err != nil {
				return nil, err
			}
			row[3] = sqltype.String(source.Columns[3].Usage.Type, member.Label)
			rows[at] = row
			at++
		}
	}
	return rows, nil
}
