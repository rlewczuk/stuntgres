package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ProgressBasebackupOID is a native relation identity, not an initdb view OID.
const ProgressBasebackupOID ids.OID = 12017

var progressBasebackupColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"phase", sqltype.Text, true, 0, 0, -1},
	{"backup_total", sqltype.Int8, true, 0, 0, -1},
	{"backup_streamed", sqltype.Int8, true, 0, 0, -1},
	{"tablespaces_total", sqltype.Int8, true, 0, 0, -1},
	{"tablespaces_streamed", sqltype.Int8, true, 0, 0, -1},
}

func captureBasebackupProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressBasebackupColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) {
		return nil, fmt.Errorf("stuntgres: unregistered basebackup progress source")
	}
	// There are no physical BASE_BACKUP workers. Native logical Dump is not
	// physical backup progress. Nonnil emptiness retains an anonymous SQL source.
	return [][]sqltype.Value{}, nil
}
