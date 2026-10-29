package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const PublicationNamespaceOID ids.OID = 6237

func capturePGPublicationNamespace(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: PublicationNamespaceOID}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid publication namespace source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, PublicationNamespaceOID); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, d := range in.Catalog.PublicationNamespaces() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.PublicationNamespaceProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 3)
		for i, oid := range []ids.OID{d.Address().ObjectID, d.Publication.ObjectID, d.Schema.ObjectID} {
			var err error
			row[i], err = sqltype.OIDValue(in.Source.Columns[i].Usage.Type, uint32(oid))
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
