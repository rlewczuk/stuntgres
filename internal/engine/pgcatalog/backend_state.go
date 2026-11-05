package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// BackendStateValue projects a detached raw cut; it never resolves a live slot.
// Waits, when requested, have already been overlaid by the lock-owning host.
func BackendStateValue(in Input, kind functions.BackendStateKind, id int64, result *sqltype.Descriptor) (sqltype.Value, error) {
	if kind.Name() == "" || result == nil || result.ID() != kind.ResultType() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid backend state projection")
	}
	if kind == functions.BackendStateSubxact {
		if result.CompositeLen() != 2 {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid backend subxact shape")
		}
		for i, name := range []string{"subxact_count", "subxact_overflowed"} {
			m := result.CompositeMember(i)
			typ := sqltype.Int4
			if i == 1 {
				typ = sqltype.Bool
			}
			if m.Attribute != int32(i+1) || m.Name != name || m.Usage.Type == nil || m.Usage.Type.ID() != typ {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid backend subxact member")
			}
		}
	}
	if err := in.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	var all bool
	if kind != functions.BackendStateSubxact {
		var err error
		all, err = activityAllStats(in)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, a := range in.Activities {
		if err := in.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if int64(a.BackendID) != id {
			continue
		}
		if kind == functions.BackendStateSubxact {
			if err := in.Roles.ValidatePrincipal(a.Principal); err != nil {
				return sqltype.Value{}, err
			}
			count, err := sqltype.Integer(result.CompositeMember(0).Usage.Type, int64(a.SubxactCount))
			if err != nil {
				return sqltype.Value{}, err
			}
			return sqltype.OwnedCompositeValue(result, []sqltype.Value{count, sqltype.Boolean(result.CompositeMember(1).Usage.Type, a.SubxactOverflowed)})
		}
		_, _, allowed, err := activityAccess(in, a.Principal, all)
		if err != nil {
			return sqltype.Value{}, err
		}
		if !allowed {
			if result.ID() == sqltype.Text {
				return sqltype.String(result, "<insufficient privilege>"), nil
			}
			return sqltype.Null(result), nil
		}
		switch kind {
		case functions.BackendStateActivity:
			query := a.Query
			if query == "" {
				query = "<command string not enabled>"
			}
			return sqltype.String(result, query), nil
		case functions.BackendStateActivityStart:
			if !a.Start.IsZero() {
				return sqltype.TimestampFromTime(result, a.Start.UTC())
			}
		case functions.BackendStateXactStart:
			if !a.TransactionStart.IsZero() {
				return sqltype.TimestampFromTime(result, a.TransactionStart.UTC())
			}
		case functions.BackendStateWaitEvent:
			if a.Wait != "" {
				return sqltype.String(result, a.Wait), nil
			}
		case functions.BackendStateWaitEventType:
			if a.WaitType != "" {
				return sqltype.String(result, a.WaitType), nil
			}
		}
		return sqltype.Null(result), nil
	}
	if kind == functions.BackendStateSubxact {
		return sqltype.OwnedCompositeValue(result, []sqltype.Value{sqltype.Null(result.CompositeMember(0).Usage.Type), sqltype.Null(result.CompositeMember(1).Usage.Type)})
	}
	if result.ID() == sqltype.Text {
		return sqltype.String(result, "<backend information not available>"), nil
	}
	return sqltype.Null(result), nil
}
