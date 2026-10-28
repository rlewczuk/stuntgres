package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ProgressCopyOID is a native source identity, not an initdb-generated view OID.
const ProgressCopyOID ids.OID = 12199

// ProgressCopyRuleOID is the native identity of COPY progress's released _RETURN
// metadata. PostgreSQL's initdb-generated rewrite OID is not a portable constant.
const ProgressCopyRuleOID ids.OID = 12200

type CopyProgressFact struct {
	PID                                                            int64
	Database                                                       ids.OID
	DatabaseName                                                   string
	DatabaseExists                                                 bool
	Relation                                                       ids.OID
	Command                                                        string
	BytesProcessed, TuplesProcessed, TuplesExcluded, TuplesSkipped int64
	Visible                                                        bool
}

var progressCopyColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"datid", sqltype.Oid, true, 0, 0, -1},
	{"datname", sqltype.Name, true, 0, 0, -1},
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"command", sqltype.Text, true, 950, 0, -1},
	{"type", sqltype.Text, true, 950, 0, -1},
	{"bytes_processed", sqltype.Int8, true, 0, 0, -1},
	{"bytes_total", sqltype.Int8, true, 0, 0, -1},
	{"tuples_processed", sqltype.Int8, true, 0, 0, -1},
	{"tuples_excluded", sqltype.Int8, true, 0, 0, -1},
	{"tuples_skipped", sqltype.Int8, true, 0, 0, -1},
}

func captureCopyProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressCopyColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	err := in.Runtime.VisitCopyProgress(in.Context, func(f CopyProgressFact) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(progressCopyColumns))
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
			row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, f.Command)
			row[5] = sqltype.String(in.Source.Columns[5].Usage.Type, "PIPE")
			counters := [...]int64{f.BytesProcessed, 0, f.TuplesProcessed, f.TuplesExcluded, f.TuplesSkipped}
			for i, n := range counters {
				row[i+6], err = sqltype.Integer(in.Source.Columns[i+6].Usage.Type, n)
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
