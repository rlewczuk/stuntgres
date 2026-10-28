package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Native source/rewrite identities are not portable PostgreSQL initdb OIDs.
const ProgressCreateIndexOID ids.OID = 12203
const ProgressCreateIndexRuleOID ids.OID = 12204

type CreateIndexProgressFact struct {
	PID                                              int64
	Database, Relation, Index                        ids.OID
	DatabaseName                                     string
	DatabaseExists, Visible                          bool
	Command, Phase                                   string
	LockersTotal, LockersDone, CurrentLockerPID      int64
	BlocksTotal, BlocksDone, TuplesTotal, TuplesDone int64
	PartitionsTotal, PartitionsDone                  int64
}

var progressCreateIndexColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"datid", sqltype.Oid, true, 0, 0, -1},
	{"datname", sqltype.Name, true, 0, 0, -1},
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"index_relid", sqltype.Oid, true, 0, 0, -1},
	{"command", sqltype.Text, true, 950, 0, -1},
	{"phase", sqltype.Text, true, 950, 0, -1},
	{"lockers_total", sqltype.Int8, true, 0, 0, -1},
	{"lockers_done", sqltype.Int8, true, 0, 0, -1},
	{"current_locker_pid", sqltype.Int8, true, 0, 0, -1},
	{"blocks_total", sqltype.Int8, true, 0, 0, -1},
	{"blocks_done", sqltype.Int8, true, 0, 0, -1},
	{"tuples_total", sqltype.Int8, true, 0, 0, -1},
	{"tuples_done", sqltype.Int8, true, 0, 0, -1},
	{"partitions_total", sqltype.Int8, true, 0, 0, -1},
	{"partitions_done", sqltype.Int8, true, 0, 0, -1},
}

func captureCreateIndexProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressCreateIndexColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	err := in.Runtime.VisitCreateIndexProgress(in.Context, func(f CreateIndexProgressFact) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(progressCreateIndexColumns))
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
			for i, n := range [...]ids.OID{f.Relation, f.Index} {
				row[i+3], err = sqltype.OIDValue(in.Source.Columns[i+3].Usage.Type, uint32(n))
				if err != nil {
					return err
				}
			}
			row[5] = sqltype.String(in.Source.Columns[5].Usage.Type, f.Command)
			row[6] = sqltype.String(in.Source.Columns[6].Usage.Type, f.Phase)
			for i, n := range [...]int64{f.LockersTotal, f.LockersDone, f.CurrentLockerPID, f.BlocksTotal, f.BlocksDone, f.TuplesTotal, f.TuplesDone, f.PartitionsTotal, f.PartitionsDone} {
				row[i+7], err = sqltype.Integer(in.Source.Columns[i+7].Usage.Type, n)
				if err != nil {
					return err
				}
			}
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}
