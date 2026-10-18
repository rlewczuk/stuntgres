package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture immutable definitions from the caller's coherent catalog generation;
// catalog labels are metadata, not enum datum input (including unsafe members).
func capturePGEnum(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid enum source shape")
	}
	for i, id := range [4]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Float4, sqltype.Name} {
		owned, ok := catalog.LookupType(id)
		if !ok || owned != source.columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign enum source type")
		}
	}
	count := 0
	for address, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*typeDescriptor)
		if !ok || !d.typ.IsEnum() {
			continue
		}
		if address != d.Address() || address.ClassID != ids.TypeClassID || address.ObjectID == 0 || sqltype.ID(address.ObjectID) != d.typ.ID() {
			return nil, fmt.Errorf("stuntgres: invalid enum owner identity")
		}
		count += d.typ.EnumMemberCount()
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*4)
	at := 0
	for _, descriptor := range catalog.state.descriptors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*typeDescriptor)
		if !ok || !d.typ.IsEnum() {
			continue
		}
		for i := range d.typ.EnumMemberCount() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			member, ok := d.typ.EnumMemberAt(i)
			if !ok {
				return nil, fmt.Errorf("stuntgres: missing enum member metadata")
			}
			row := values[at*4 : (at+1)*4 : (at+1)*4]
			var err error
			row[0], err = sqltype.OIDValue(source.columns[0].Usage.Type, uint32(member.ID))
			if err != nil {
				return nil, err
			}
			row[1], err = sqltype.OIDValue(source.columns[1].Usage.Type, uint32(d.typ.ID()))
			if err != nil {
				return nil, err
			}
			row[2], err = sqltype.Float(source.columns[2].Usage.Type, float64(member.SortOrder))
			if err != nil {
				return nil, err
			}
			row[3] = sqltype.String(source.columns[3].Usage.Type, member.Label)
			rows[at] = row
			at++
		}
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
