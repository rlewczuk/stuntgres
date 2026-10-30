package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const ReplicationOriginOID ids.OID = 6000

func capturePGReplicationOrigin(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ReplicationOriginOID}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid replication origin source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, ReplicationOriginOID); err != nil {
		return nil, err
	}
	if err := catalog.ValidateReplicationOrigins(in.Catalog); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, d := range in.Catalog.ReplicationOrigins() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.ReplicationOriginProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{id, sqltype.String(in.Source.Columns[1].Usage.Type, d.Name())})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
