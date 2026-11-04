package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatActivityOID ids.OID = 12226

var activityColumns = []ColumnSpec{
	{"datid", sqltype.Oid, true, 0, 0, -1}, {"datname", sqltype.Name, true, 950, 0, -1},
	{"pid", sqltype.Int4, true, 0, 0, -1}, {"leader_pid", sqltype.Int4, true, 0, 0, -1},
	{"usesysid", sqltype.Oid, true, 0, 0, -1}, {"usename", sqltype.Name, true, 950, 0, -1},
	{"application_name", sqltype.Text, true, 950, 0, -1}, {"client_addr", sqltype.Inet, true, 0, 0, -1},
	{"client_hostname", sqltype.Text, true, 950, 0, -1}, {"client_port", sqltype.Int4, true, 0, 0, -1},
	{"backend_start", sqltype.Timestamptz, true, 0, 0, -1}, {"xact_start", sqltype.Timestamptz, true, 0, 0, -1},
	{"query_start", sqltype.Timestamptz, true, 0, 0, -1}, {"state_change", sqltype.Timestamptz, true, 0, 0, -1},
	{"wait_event_type", sqltype.Text, true, 950, 0, -1}, {"wait_event", sqltype.Text, true, 950, 0, -1},
	{"state", sqltype.Text, true, 950, 0, -1}, {"backend_xid", sqltype.Xid, true, 0, 0, -1},
	{"backend_xmin", sqltype.Xid, true, 0, 0, -1}, {"query_id", sqltype.Int8, true, 0, 0, -1},
	{"query", sqltype.Text, true, 950, 0, -1}, {"backend_type", sqltype.Text, true, 950, 0, -1},
}

func capturePGStatActivity(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatActivityOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid activity source authority")
	}
	allStats, err := activityAllStats(in)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, len(in.Activities))
	for token, activity := range in.Activities {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		role, present, allowed, err := activityAccess(in, activity.Principal, allStats)
		if err != nil {
			return nil, err
		}
		row, err := activityRow(in, token, activity, allowed, role.Name, present)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func activityRow(in Input, token ids.SessionToken, a statistics.Activity, allowed bool, userName string, userPresent bool) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, 22)
	for i := range row {
		row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
	}
	integer := func(i int, n int64) error {
		var v sqltype.Value
		var err error
		typ := in.Source.Columns[i].Usage.Type
		if typ.ID() == sqltype.Oid {
			v, err = sqltype.OIDValue(typ, uint32(n))
		} else {
			v, err = sqltype.Integer(typ, n)
		}
		row[i] = v
		return err
	}
	text := func(i int, s string) { row[i] = sqltype.String(in.Source.Columns[i].Usage.Type, s) }
	stamp := func(i int, t time.Time) error {
		if t.IsZero() {
			return nil
		}
		v, err := sqltype.TimestampFromTime(in.Source.Columns[i].Usage.Type, t.UTC())
		row[i] = v
		return err
	}
	if err := integer(0, int64(a.Database)); err != nil {
		return nil, err
	}
	text(1, a.DatabaseName)
	if err := integer(2, int64(token.ID())); err != nil {
		return nil, err
	}
	if err := integer(4, int64(a.Principal.ID())); err != nil {
		return nil, err
	}
	if userPresent {
		text(5, userName)
	}
	text(6, a.Application)
	for _, x := range []struct {
		column int
		xid    uint32
	}{{17, a.XID}, {18, a.Xmin}} {
		if x.xid != 0 {
			v, err := sqltype.XIDValue(in.Source.Columns[x.column].Usage.Type, x.xid)
			if err != nil {
				return nil, err
			}
			row[x.column] = v
		}
	}
	if !allowed {
		redactActivity(row)
		return row, nil
	}
	for _, s := range []struct {
		column int
		value  time.Time
	}{{10, a.BackendStart}, {11, a.TransactionStart}, {12, a.Start}, {13, a.StateChange}} {
		if err := stamp(s.column, s.value); err != nil {
			return nil, err
		}
	}
	if a.WaitType != "" {
		text(14, a.WaitType)
		text(15, a.Wait)
	}
	text(16, a.State)
	if a.QueryID != 0 {
		if err := integer(19, a.QueryID); err != nil {
			return nil, err
		}
	}
	if a.QueryAvailable {
		text(20, a.Query)
	}
	text(21, a.BackendType)
	return row, nil
}

func redactActivity(row []sqltype.Value) {
	for _, i := range [...]int{3, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 19, 21} {
		row[i] = sqltype.Null(row[i].Type())
	}
	row[20] = sqltype.String(row[20].Type(), "<insufficient privilege>")
}
