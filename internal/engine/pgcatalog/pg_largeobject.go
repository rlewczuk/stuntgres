package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
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
