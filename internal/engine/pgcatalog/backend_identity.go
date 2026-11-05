package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// BackendIdentityIDs consumes the existing detached unmasked status cut. Slots
// are physical identities, not sorted enumeration positions or process IDs.
func BackendIdentityIDs(in Input, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if result == nil || result.ID() != sqltype.Int4 {
		return nil, fmt.Errorf("stuntgres: invalid backend ID set type")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if _, err := in.Roles.Descriptor(in.ActivityObserver); err != nil {
		return nil, err
	}
	values := make([]sqltype.Value, 0, len(in.Activities))
	for _, a := range in.Activities {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := in.Roles.ValidatePrincipal(a.Principal); err != nil {
			return nil, err
		}
		value, err := sqltype.Integer(result, int64(a.BackendID))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// BackendIdentityValue never joins through live session membership: retirement
// and slot reuse cannot replace an entry already captured by this transaction.
func BackendIdentityValue(in Input, kind functions.BackendIdentityKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if kind == functions.BackendIDSet || kind.Name() == "" || result == nil || result.ID() != kind.ResultType() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid backend identity projection")
	}
	if err := in.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	all, err := activityAllStats(in)
	if err != nil {
		return sqltype.Value{}, err
	}
	for token, a := range in.Activities {
		if err := in.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if int64(a.BackendID) != id {
			continue
		}
		_, _, allowed, err := activityAccess(in, a.Principal, all)
		if err != nil {
			return sqltype.Value{}, err
		}
		switch kind {
		case functions.BackendPID:
			return sqltype.Integer(result, int64(token.ID()))
		case functions.BackendDatabaseID:
			return sqltype.OIDValue(result, uint32(a.Database))
		case functions.BackendUserID:
			return sqltype.OIDValue(result, uint32(a.Principal.ID()))
		case functions.BackendStart:
			if allowed && !a.BackendStart.IsZero() {
				return sqltype.TimestampFromTime(result, a.BackendStart.UTC())
			}
		case functions.BackendClientAddress, functions.BackendClientPort:
			// In-process public attachments have no socket endpoint. In
			// particular, -1 belongs to an actual Unix socket, not this host.
		}
		break
	}
	return sqltype.Null(result), nil
}
