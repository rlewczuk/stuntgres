package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatXactAllTablesOID ids.OID = 12151
const StatXactSysTablesOID ids.OID = 12161
const StatXactUserTablesOID ids.OID = 12170

// XactRelationFact is a detached owner-local value, not cumulative history.
// Index searches/simple fetches use index identities; bitmap fetches use tables.
type XactRelationFact struct {
	SeqScans, SeqTuples, IndexScans, IndexFetches, TableIndexFetches int64
	Inserts, Updates, Deletes                                        int64
}

var xactTableStatisticsColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 0, 0, -1},
	{"relname", sqltype.Name, true, 0, 0, -1},
	{"seq_scan", sqltype.Int8, true, 0, 0, -1},
	{"seq_tup_read", sqltype.Int8, true, 0, 0, -1},
	{"idx_scan", sqltype.Int8, true, 0, 0, -1},
	{"idx_tup_fetch", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_ins", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_upd", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_del", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_hot_upd", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_newpage_upd", sqltype.Int8, true, 0, 0, -1},
}

func captureXactTableStatistics(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, xactTableStatisticsColumns); err != nil {
		return nil, err
	}
	indexes := make(map[ids.ObjectAddress][]ids.ObjectAddress)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if d, ok := descriptor.(*catalog.Relation); ok && d.SQLIndex != nil {
			indexes[d.SQLIndex.Parent] = append(indexes[d.SQLIndex.Parent], d.Address())
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		kind := ClassKind(in.Catalog, d)
		if kind != "r" && kind != "t" && kind != "m" && kind != "p" {
			continue
		}
		ns, ok := in.Catalog.Namespace(d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing transaction statistics namespace")
		}
		if source := in.Source.Address().ObjectID; source == StatXactSysTablesOID || source == StatXactUserTablesOID {
			system := ns.Name() == "pg_catalog" || ns.Name() == "information_schema" || strings.HasPrefix(ns.Name(), "pg_toast")
			if system != (source == StatXactSysTablesOID) {
				continue
			}
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		schema := sqltype.String(in.Source.Columns[1].Usage.Type, ns.Name())
		name := sqltype.String(in.Source.Columns[2].Usage.Type, d.Name())
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return oid, nil }) || in.Keep.Rejects(1, func() (sqltype.Value, error) { return schema, nil }) || in.Keep.Rejects(2, func() (sqltype.Value, error) { return name, nil }) {
			continue
		}
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		fact := in.Runtime.XactRelationStatistics(d.Address())
		row := make([]sqltype.Value, len(xactTableStatisticsColumns))
		row[0], row[1], row[2] = oid, schema, name
		// Native HOT and new-page placement are deterministic physical estimates.
		counts := [...]struct {
			column int
			value  int64
		}{{3, fact.SeqScans}, {4, fact.SeqTuples}, {7, fact.Inserts}, {8, fact.Updates}, {9, fact.Deletes}, {10, 0}, {11, 0}}
		for _, count := range counts {
			row[count.column], err = sqltype.Integer(in.Source.Columns[count.column].Usage.Type, count.value)
			if err != nil {
				return nil, err
			}
		}
		row[5] = sqltype.Null(in.Source.Columns[5].Usage.Type)
		row[6] = sqltype.Null(in.Source.Columns[6].Usage.Type)
		if attached := indexes[d.Address()]; len(attached) != 0 {
			var scans, fetches int64
			for _, address := range attached {
				if err := in.Context.Err(); err != nil {
					return nil, err
				}
				index := in.Runtime.XactRelationStatistics(address)
				scans += index.IndexScans
				fetches += index.IndexFetches
			}
			row[5], err = sqltype.Integer(in.Source.Columns[5].Usage.Type, scans)
			if err != nil {
				return nil, err
			}
			row[6], err = sqltype.Integer(in.Source.Columns[6].Usage.Type, fetches+fact.TableIndexFetches)
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
