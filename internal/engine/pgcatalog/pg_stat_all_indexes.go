package pgcatalog

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"time"
)

// Relation identity follows docs/design/regtype-pg18-inventory.tsv, not a
// claim that PostgreSQL initdb-generated view OIDs are portable.
const StatAllIndexesOID ids.OID = 12187
const StatSysIndexesOID ids.OID = 12192

type IndexStatisticsFact struct {
	Scans, Entries, Fetches int64
	LastScan                time.Time
}

var indexStatisticsColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1}, {"indexrelid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 0, 0, -1}, {"relname", sqltype.Name, true, 0, 0, -1}, {"indexrelname", sqltype.Name, true, 0, 0, -1},
	{"idx_scan", sqltype.Int8, true, 0, 0, -1}, {"last_idx_scan", sqltype.Timestamptz, true, 0, 0, -1},
	{"idx_tup_read", sqltype.Int8, true, 0, 0, -1}, {"idx_tup_fetch", sqltype.Int8, true, 0, 0, -1},
}

func captureIndexStatistics(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, indexStatisticsColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent, ok := in.Catalog.Relation(index.SQLIndex.Parent)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing statistics index parent")
		}
		kind := ClassKind(in.Catalog, parent)
		if kind != "r" && kind != "t" && kind != "m" {
			continue
		}
		ns, ok := in.Catalog.Namespace(parent.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing statistics index namespace")
		}
		if in.Source.Address().ObjectID == StatSysIndexesOID && ns.Name() != "pg_catalog" && ns.Name() != "information_schema" && !strings.HasPrefix(ns.Name(), "pg_toast") {
			continue
		}
		row := make([]sqltype.Value, len(indexStatisticsColumns))
		var err error
		row[0], err = sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(parent.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[1], err = sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(index.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, ns.Name())
		row[3] = sqltype.String(in.Source.Columns[3].Usage.Type, parent.Name())
		row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, index.Name())
		rejected := false
		for column := range 5 {
			if in.Keep.Rejects(column, func() (sqltype.Value, error) { return row[column], nil }) {
				rejected = true
				break
			}
		}
		if rejected {
			continue
		}
		// This view has no parent ACL filter or per-field privilege redaction.
		// Keep must precede this shared Reader access in cache mode.
		fact := in.Runtime.IndexStatistics(index.Address())
		for _, counter := range [...]struct {
			column int
			value  int64
		}{{5, fact.Scans}, {7, fact.Entries}, {8, fact.Fetches}} {
			row[counter.column], err = sqltype.Integer(in.Source.Columns[counter.column].Usage.Type, counter.value)
			if err != nil {
				return nil, err
			}
		}
		row[6] = sqltype.Null(in.Source.Columns[6].Usage.Type)
		if !fact.LastScan.IsZero() {
			row[6], err = sqltype.TimestampFromTime(in.Source.Columns[6].Usage.Type, fact.LastScan)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
