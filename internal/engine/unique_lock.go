package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqltype"
)

// pinUniqueKey hashes the claimed values, then pins their interned lock
// resource. Hashing and comparison run outside every metadata mutex.
func (e *Engine) pinUniqueKey(ctx context.Context, ticket *lock.Ticket, ref ids.ObjectRef, ordering index.Ordering, grouping []sqltype.Grouping, values []sqltype.Value) (lock.Resource, func(), error) {
	hash, err := index.UniqueKeyHash(ctx, ordering, grouping, values)
	if err != nil {
		return lock.Resource{}, nil, err
	}
	return e.locks.PinUniqueKey(ctx, ticket, ref, hash, &index.UniqueKeyValues{Ordering: ordering, Values: values})
}
