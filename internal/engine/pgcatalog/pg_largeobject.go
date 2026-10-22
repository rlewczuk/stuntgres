package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/sqltype"
)

func capturePGLargeObjectMetadata(in Input) ([][]sqltype.Value, error) {
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		d, ok := descriptor.(*catalog.LargeObject)
		if !ok {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(in.Roles, d); err != nil {
			return nil, err
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(d.Owner.ID()))
		if err != nil {
			return nil, err
		}
		acl, err := ClassACL(in.Owner, in.Roles, in.Source.Columns[2].Usage.Type, d.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, owner, acl})
	}
	return rows, nil
}

// Project stored chunks only: holes and short tails are physical page facts,
// not the zero-filled logical reads returned by lo_get.
func capturePGLargeObject(in Input) ([][]sqltype.Value, error) {
	rows := make([][]sqltype.Value, 0)
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || in.Catalog.State() == nil || in.Source == nil ||
		in.Source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 2613}) ||
		in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() ||
		in.Ref.Address != in.Source.Address() || in.Catalog.Get(in.Source.Address()) != in.Source ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: foreign large object page source authority")
	}
	types := [...]sqltype.ID{sqltype.Oid, sqltype.Int4, sqltype.Bytea}
	if len(in.Source.Columns) != len(types) {
		return nil, fmt.Errorf("stuntgres: invalid large object page source shape")
	}
	for i, id := range types {
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != in.Source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign large object page source type")
		}
	}
	for address, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.LargeObject)
		if !ok {
			continue
		}
		if err := (catalog.LargeObjectProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		if address != d.Address() {
			return nil, fmt.Errorf("stuntgres: invalid large object page address authority")
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		d.Content.Chunks(func(offset int64, data []byte) bool {
			if err = in.Context.Err(); err != nil {
				return false
			}
			var page, value sqltype.Value
			page, err = sqltype.Integer(in.Source.Columns[1].Usage.Type, offset/largeobject.PageSize)
			if err != nil {
				return false
			}
			value, err = sqltype.Binary(in.Source.Columns[2].Usage.Type, string(data))
			if err != nil {
				return false
			}
			rows = append(rows, []sqltype.Value{oid, page, value})
			return true
		})
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
