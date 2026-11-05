package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

// BackendStatisticsKind is the released participation classifier, not a list of
// processes to create. Activity may contain other genuine backend kinds.
func BackendStatisticsKind(kind string) bool {
	switch kind {
	case "autovacuum worker", "client backend", "background worker", "standalone backend", "slotsync worker", "walreceiver", "walsender", "walsummarizer", "walwriter":
		return true
	}
	return false
}

// BackendActivity projects the same unmasked status cut and current inherited
// authority as pg_stat_activity, with the callable's distinct fixed OUT layout.
func BackendActivity(in Input, pid int64, shape *sqltype.Descriptor) ([]sqltype.Value, error) {
	all, err := activityAllStats(in)
	if err != nil {
		return nil, err
	}
	values := make([]sqltype.Value, 0, len(in.Activities))
	for token, a := range in.Activities {
		if pid != -1 && int64(token.ID()) != pid {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		_, _, allowed, err := activityAccess(in, a.Principal, all)
		if err != nil {
			return nil, err
		}
		row, err := backendActivityRow(shape, token, a, allowed)
		if err != nil {
			return nil, err
		}
		value, err := sqltype.OwnedCompositeValue(shape, row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func backendActivityRow(shape *sqltype.Descriptor, token ids.SessionToken, a statistics.Activity, allowed bool) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, 31)
	for i := range row {
		row[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
	}
	integer := func(i int, n int64) error {
		typ := row[i].Type()
		var value sqltype.Value
		var err error
		if typ.ID() == sqltype.Oid {
			value, err = sqltype.OIDValue(typ, uint32(n))
		} else {
			value, err = sqltype.Integer(typ, n)
		}
		row[i] = value
		return err
	}
	text := func(i int, s string) { row[i] = sqltype.String(row[i].Type(), s) }
	for _, field := range []struct {
		index int
		value int64
	}{{0, int64(a.Database)}, {1, int64(token.ID())}, {2, int64(a.Principal.ID())}} {
		if err := integer(field.index, field.value); err != nil {
			return nil, err
		}
	}
	text(3, a.Application)
	for _, field := range []struct {
		index int
		value uint32
	}{{15, a.XID}, {16, a.Xmin}} {
		if field.value != 0 {
			value, err := sqltype.XIDValue(row[field.index].Type(), field.value)
			if err != nil {
				return nil, err
			}
			row[field.index] = value
		}
	}
	if !allowed {
		text(5, "<insufficient privilege>")
		return row, nil
	}
	text(4, a.State)
	if a.QueryAvailable {
		text(5, a.Query)
	}
	if a.WaitType != "" {
		text(6, a.WaitType)
		text(7, a.Wait)
	}
	for _, field := range []struct {
		index int
		value time.Time
	}{{8, a.TransactionStart}, {9, a.Start}, {10, a.BackendStart}, {11, a.StateChange}} {
		if !field.value.IsZero() {
			value, err := sqltype.TimestampFromTime(row[field.index].Type(), field.value.UTC())
			if err != nil {
				return nil, err
			}
			row[field.index] = value
		}
	}
	text(17, a.BackendType)
	for _, index := range [...]int{18, 25, 27, 28} {
		row[index] = sqltype.Boolean(row[index].Type(), false)
	}
	if a.QueryID != 0 {
		if err := integer(30, a.QueryID); err != nil {
			return nil, err
		}
	}
	return row, nil
}
