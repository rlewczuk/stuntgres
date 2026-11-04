package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Native allocations follow docs/design/regtype-pg18-inventory.tsv.
const StatSubscriptionStatsOID ids.OID = 12347
const StatSubscriptionStatsRuleOID ids.OID = 12350

var statSubscriptionStatsColumns = []ColumnSpec{
	{"subid", sqltype.Oid, true, 0, 0, -1},
	{"subname", sqltype.Name, true, 950, 0, -1},
	{"apply_error_count", sqltype.Int8, true, 0, 0, -1},
	{"sync_error_count", sqltype.Int8, true, 0, 0, -1},
	{"confl_insert_exists", sqltype.Int8, true, 0, 0, -1},
	{"confl_update_origin_differs", sqltype.Int8, true, 0, 0, -1},
	{"confl_update_exists", sqltype.Int8, true, 0, 0, -1},
	{"confl_update_missing", sqltype.Int8, true, 0, 0, -1},
	{"confl_delete_origin_differs", sqltype.Int8, true, 0, 0, -1},
	{"confl_delete_missing", sqltype.Int8, true, 0, 0, -1},
	{"confl_multiple_unique_conflicts", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatSubscriptionStats(in Input) ([][]sqltype.Value, error) {
	if in.Context == nil {
		return nil, fmt.Errorf("stuntgres: invalid subscription cumulative context")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatSubscriptionStatsOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid subscription cumulative source authority")
	}
	for i, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != statSubscriptionStatsColumns[i].Collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid subscription cumulative source attribute")
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
		name := sqltype.String(in.Source.Columns[StatSubscriptionStatsNameColumn].Usage.Type, subscription.Name())
		if in.Keep.Rejects(StatSubscriptionStatsNameColumn, func() (sqltype.Value, error) { return name, nil }) {
			continue
		}
		row := make([]sqltype.Value, len(statSubscriptionStatsColumns))
		var err error
		row[0], err = sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(subscription.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[StatSubscriptionStatsNameColumn] = name
		fact := in.SubscriptionStats[subscription.Address()]
		counts := [...]int64{fact.ApplyErrorCount, fact.SyncErrorCount, fact.ConflInsertExists, fact.ConflUpdateOriginDiffers, fact.ConflUpdateExists, fact.ConflUpdateMissing, fact.ConflDeleteOriginDiffers, fact.ConflDeleteMissing, fact.ConflMultipleUniqueConflicts}
		for i, count := range counts {
			row[i+2], err = sqltype.Integer(in.Source.Columns[i+2].Usage.Type, count)
			if err != nil {
				return nil, err
			}
		}
		row[11] = sqltype.Null(in.Source.Columns[11].Usage.Type)
		if !fact.ResetAt.IsZero() {
			row[11], err = sqltype.TimestampFromTime(in.Source.Columns[11].Usage.Type, fact.ResetAt)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// Released lateral getter text is metadata, not a callable SQL implementation.
var statSubscriptionStatsViewParts = subscriptionStatsViewParts()

func subscriptionStatsViewParts() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT "), viewIdentifier("ss"), viewText("."), viewIdentifier("subid"), viewText(",\n    "), viewIdentifier("s"), viewText("."), viewIdentifier("subname")}
	for _, column := range statSubscriptionStatsColumns[2:] {
		parts = append(parts, viewText(",\n    "), viewIdentifier("ss"), viewText("."), viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewClass("pg_subscription"), viewText(" "), viewIdentifier("s"), viewText(",\n    LATERAL "), viewRoutine("pg_stat_get_subscription_stats", "26"), viewText("("), viewIdentifier("s"), viewText("."), viewIdentifier("oid"), viewText(") "), viewIdentifier("ss"), viewText("("))
	for i, column := range statSubscriptionStatsColumns {
		if i == 1 {
			continue
		}
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}
