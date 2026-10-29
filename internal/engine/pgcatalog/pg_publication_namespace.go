package pgcatalog

import (
	"fmt"

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
	// Execute's current used-column manifest owns SELECT admission. Publication
	// commands own schema memberships; no local memberships currently exist.
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{}, nil
}
