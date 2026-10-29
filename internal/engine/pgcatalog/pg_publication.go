package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const PublicationOID ids.OID = 6104

func capturePGPublication(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: PublicationOID}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid publication source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, PublicationOID); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, d := range in.Catalog.Publications() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.PublicationProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(in.Roles, d); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 10)
		for _, f := range []struct {
			slot int
			oid  uint32
		}{{0, uint32(d.Address().ObjectID)}, {2, uint32(d.Owner.ID())}} {
			var err error
			row[f.slot], err = sqltype.OIDValue(in.Source.Columns[f.slot].Usage.Type, f.oid)
			if err != nil {
				return nil, err
			}
		}
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, d.Name())
		for i, value := range []bool{d.AllTables, d.Insert, d.Update, d.Delete, d.Truncate, d.ViaRoot} {
			row[i+3] = sqltype.Boolean(in.Source.Columns[i+3].Usage.Type, value)
		}
		row[9] = sqltype.String(in.Source.Columns[9].Usage.Type, string(d.GeneratedColumns))
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
