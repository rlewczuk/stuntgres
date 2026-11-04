package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const ReplicationOriginStatusOID ids.OID = 12343

var replicationOriginStatusViewDefinitionParts = replicationOriginStatusViewParts()

func capturePGReplicationOriginStatus(in Input) ([][]sqltype.Value, error) {
	if in.Context == nil {
		return nil, fmt.Errorf("stuntgres: invalid replication origin status context")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ReplicationOriginStatusOID}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid replication origin status source")
	}
	for i, column := range in.Source.Columns {
		collation := uint32(0)
		if i == 1 {
			collation = 100
		}
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid replication origin status attribute")
		}
	}
	rows := make([][]sqltype.Value, 0, len(in.ReplicationOriginProgress))
	for oid, progress := range in.ReplicationOriginProgress {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(oid))
		if err != nil {
			return nil, err
		}
		name := sqltype.Null(in.Source.Columns[1].Usage.Type)
		if d, ok := in.Catalog.Get(ids.ObjectAddress{ClassID: ids.ReplicationOriginClassID, ObjectID: oid}).(*catalog.ReplicationOrigin); ok {
			if err := (catalog.ReplicationOriginProvider{}).Validate(in.Catalog, d); err != nil {
				return nil, err
			}
			name = sqltype.String(in.Source.Columns[1].Usage.Type, d.Name())
		}
		remote, err := sqltype.PGLSNValue(in.Source.Columns[2].Usage.Type, progress)
		if err != nil {
			return nil, err
		}
		// No native local WAL commit position is assigned. Invalid/unset is
		// exactly zero, never a remote position, XID or timestamp surrogate.
		local, err := sqltype.PGLSNValue(in.Source.Columns[3].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{id, name, remote, local})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func replicationOriginStatusViewParts() []builtinViewPart {
	names := [...]string{"local_id", "external_id", "remote_lsn", "local_lsn"}
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, name := range names {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_show_replication_origin_status", ""), viewText("() "), viewIdentifier("pg_show_replication_origin_status"), viewText("("))
	for i, name := range names {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	return append(parts, viewText(");"))
}
