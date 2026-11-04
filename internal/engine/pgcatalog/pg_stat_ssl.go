package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatSSLOID ids.OID = 12253

var sslColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"ssl", sqltype.Bool, true, 0, 0, -1},
	{"version", sqltype.Text, true, 100, 0, -1},
	{"cipher", sqltype.Text, true, 100, 0, -1},
	{"bits", sqltype.Int4, true, 0, 0, -1},
	{"client_dn", sqltype.Text, true, 100, 0, -1},
	{"client_serial", sqltype.Numeric, true, 0, 0, -1},
	{"issuer_dn", sqltype.Text, true, 100, 0, -1},
}

func capturePGStatSSL(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatSSLOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid SSL source authority")
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
		_, _, allowed, err := activityAccess(in, activity.Principal, allStats)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		// Native sessions have no TLS transport. Authorization is equivalent to
		// the released client_port predicate, without inventing native endpoints.
		row := make([]sqltype.Value, len(sslColumns))
		row[0], err = sqltype.Integer(in.Source.Columns[0].Usage.Type, int64(token.ID()))
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.Boolean(in.Source.Columns[1].Usage.Type, false)
		for i := 2; i < len(row); i++ {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
