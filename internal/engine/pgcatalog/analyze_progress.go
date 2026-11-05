package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Native source/rewrite identities, independent of PostgreSQL initdb allocation.
const ProgressAnalyzeOID ids.OID = 12363
const ProgressAnalyzeRuleOID ids.OID = 12364

// AnalyzeProgressFact is a detached coherent snapshot, not retained authority.
type AnalyzeProgressFact struct {
	PID                                       int64
	Database, Relation, CurrentChild          ids.OID
	DatabaseName                              string
	DatabaseExists, Visible                   bool
	Phase                                     string
	SampleBlocksTotal, SampleBlocksScanned    int64
	ExtendedStatsTotal, ExtendedStatsComputed int64
	ChildTablesTotal, ChildTablesDone         int64
	DelayTime                                 float64
}

var progressAnalyzeColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"datid", sqltype.Oid, true, 0, 0, -1},
	{"datname", sqltype.Name, true, 0, 0, -1},
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"phase", sqltype.Text, true, 100, 0, -1},
	{"sample_blks_total", sqltype.Int8, true, 0, 0, -1},
	{"sample_blks_scanned", sqltype.Int8, true, 0, 0, -1},
	{"ext_stats_total", sqltype.Int8, true, 0, 0, -1},
	{"ext_stats_computed", sqltype.Int8, true, 0, 0, -1},
	{"child_tables_total", sqltype.Int8, true, 0, 0, -1},
	{"child_tables_done", sqltype.Int8, true, 0, 0, -1},
	{"current_child_table_relid", sqltype.Oid, true, 0, 0, -1},
	{"delay_time", sqltype.Float8, true, 0, 0, -1},
}

func captureAnalyzeProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressAnalyzeColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != ProgressAnalyzeOID {
		return nil, fmt.Errorf("stuntgres: unregistered ANALYZE progress source")
	}
	rows := make([][]sqltype.Value, 0)
	err := in.Runtime.VisitAnalyzeProgress(in.Context, func(f AnalyzeProgressFact) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(progressAnalyzeColumns))
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		var err error
		row[0], err = sqltype.Integer(in.Source.Columns[0].Usage.Type, f.PID)
		if err != nil {
			return err
		}
		row[1], err = sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(f.Database))
		if err != nil {
			return err
		}
		if f.DatabaseExists {
			row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, f.DatabaseName)
		}
		if f.Visible {
			row[3], err = sqltype.OIDValue(in.Source.Columns[3].Usage.Type, uint32(f.Relation))
			if err != nil {
				return err
			}
			row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, f.Phase)
			for i, n := range [...]int64{f.SampleBlocksTotal, f.SampleBlocksScanned, f.ExtendedStatsTotal, f.ExtendedStatsComputed, f.ChildTablesTotal, f.ChildTablesDone} {
				row[i+5], err = sqltype.Integer(in.Source.Columns[i+5].Usage.Type, n)
				if err != nil {
					return err
				}
			}
			row[11], err = sqltype.OIDValue(in.Source.Columns[11].Usage.Type, uint32(f.CurrentChild))
			if err != nil {
				return err
			}
			row[12], err = sqltype.Float(in.Source.Columns[12].Usage.Type, f.DelayTime)
			if err != nil {
				return err
			}
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}
