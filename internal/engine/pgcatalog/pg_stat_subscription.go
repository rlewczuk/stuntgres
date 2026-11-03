package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatSubscriptionOID ids.OID = 12248

// Native identity for released _RETURN metadata, not a portable initdb OID.
const StatSubscriptionRuleOID ids.OID = 12251

var statSubscriptionColumns = []ColumnSpec{
	{"subid", sqltype.Oid, true, 0, 0, -1},
	{"subname", sqltype.Name, true, 950, 0, -1},
	{"worker_type", sqltype.Text, true, 100, 0, -1},
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"leader_pid", sqltype.Int4, true, 0, 0, -1},
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"received_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"last_msg_send_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"last_msg_receipt_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"latest_end_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"latest_end_time", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatSubscription(in Input) ([][]sqltype.Value, error) {
	if in.Context == nil {
		return nil, fmt.Errorf("stuntgres: invalid subscription statistics context")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatSubscriptionOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid subscription statistics source authority")
	}
	for i, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != statSubscriptionColumns[i].Collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid subscription statistics source attribute")
		}
	}
	var rows [][]sqltype.Value
	for _, subscription := range in.Catalog.Subscriptions() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.SubscriptionProvider{}).Validate(in.Catalog, subscription); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, len(statSubscriptionColumns))
		var err error
		row[0], err = sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(subscription.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, subscription.Name())
		// The released LEFT JOIN retains the shared identity without a worker.
		// Desired Enabled state, session PIDs and cumulative subscription counters
		// are not apply execution. No native worker publisher currently exists.
		for i := 2; i < len(row); i++ {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// Display metadata only; registering the released join does not implement an SRF.
func statSubscriptionViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT "), viewIdentifier("su"), viewText("."), viewIdentifier("oid"), viewText(" AS "), viewIdentifier("subid")}
	for i, column := range statSubscriptionColumns[1:] {
		alias := "st"
		if i == 0 {
			alias = "su"
		}
		parts = append(parts, viewText(",\n    "), viewIdentifier(alias), viewText("."), viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "))
	if !pretty {
		parts = append(parts, viewText("("))
	}
	parts = append(parts, viewClass("pg_subscription"), viewText(" "), viewIdentifier("su"),
		viewText("\n     LEFT JOIN "), viewRoutine("pg_stat_get_subscription", "26"), viewText("(NULL::"), viewType("oid"), viewText(") "), viewIdentifier("st"), viewText("("))
	for i, name := range []string{"subid", "relid", "pid", "leader_pid", "received_lsn", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time", "worker_type"} {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(name))
	}
	parts = append(parts, viewText(") ON "))
	if !pretty {
		parts = append(parts, viewText("(("))
	}
	parts = append(parts, viewIdentifier("st"), viewText("."), viewIdentifier("subid"), viewText(" = "), viewIdentifier("su"), viewText("."), viewIdentifier("oid"))
	if !pretty {
		parts = append(parts, viewText(")))"))
	}
	return append(parts, viewText(";"))
}
