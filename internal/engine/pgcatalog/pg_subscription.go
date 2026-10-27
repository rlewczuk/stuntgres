package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const SubscriptionOID ids.OID = 6100

func capturePGSubscription(in Input) ([][]sqltype.Value, error) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: SubscriptionOID}
	if in.Context == nil || in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid subscription source")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, SubscriptionOID); err != nil {
		return nil, err
	}
	// Execute's used-column manifest owns admission, including hidden and
	// whole-row references. Checking every column here would deny PUBLIC reads.
	// Subscription commands own population; catalog reads never contact a publisher.
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{}, nil
}
