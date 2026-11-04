package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const (
	StatsExtOID      ids.OID = 12058
	StatsExtExprsOID ids.OID = 12063
)

// The public views have independent SELECT admission; their runtime filters
// base-table ownership/RLS without demanding access to the protected DATA table.
func capturePGStatsExtended(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Runtime == nil {
		return nil, fmt.Errorf("stuntgres: invalid extended statistics view source")
	}
	oid := in.Source.Address().ObjectID
	if oid != StatsExtOID && oid != StatsExtExprsOID {
		return nil, fmt.Errorf("stuntgres: invalid extended statistics view identity")
	}
	if err := in.Runtime.ValidateCapture(in.Catalog, in.Roles, in.Ref, in.Source, oid); err != nil {
		return nil, err
	}
	if err := in.Runtime.SelectColumns(in.Ref, in.Source); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	err := in.Runtime.VisitExtendedStatisticsView(in.Context, in.Source, func(row []sqltype.Value) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		if len(row) != len(in.Source.Columns) {
			return fmt.Errorf("stuntgres: invalid extended statistics view row")
		}
		for i, value := range row {
			if !value.ValidResult(in.Source.Columns[i].Usage) {
				return fmt.Errorf("stuntgres: invalid extended statistics view column %d", i)
			}
		}
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
