package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatioAllTablesOID links the existing native composite and array identities.
const StatioAllTablesOID ids.OID = 12174

// StatioSysTablesOID reuses the builtin system-table view's row identities.
const StatioSysTablesOID ids.OID = 12179

// StatioUserTablesOID reuses the builtin user-table view's row identities.
const StatioUserTablesOID ids.OID = 12183

var statioTableColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 950, 0, -1},
	{"relname", sqltype.Name, true, 950, 0, -1},
	{"heap_blks_read", sqltype.Int8, true, 0, 0, -1},
	{"heap_blks_hit", sqltype.Int8, true, 0, 0, -1},
	{"idx_blks_read", sqltype.Int8, true, 0, 0, -1},
	{"idx_blks_hit", sqltype.Int8, true, 0, 0, -1},
	{"toast_blks_read", sqltype.Int8, true, 0, 0, -1},
	{"toast_blks_hit", sqltype.Int8, true, 0, 0, -1},
	{"tidx_blks_read", sqltype.Int8, true, 0, 0, -1},
	{"tidx_blks_hit", sqltype.Int8, true, 0, 0, -1},
}

func captureStatioTables(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, statioTableColumns); err != nil {
		return nil, err
	}
	// Capture real relationships once, without reading statistics for rejected
	// tables (important for the per-object cache mode of the shared Reader).
	indexes := make(map[ids.ObjectAddress][]ids.ObjectAddress)
	for address, relation := range in.Catalog.Relations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if relation.SQLIndex != nil {
			parent := relation.SQLIndex.Parent
			indexes[parent] = append(indexes[parent], address)
		}
	}
	rows := make([][]sqltype.Value, 0)
	for address, relation := range in.Catalog.Relations() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if !statioIndexParentKind(ClassKind(in.Catalog, relation)) {
			continue
		}
		namespace, ok := in.Catalog.Namespace(relation.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing table block statistics namespace")
		}
		if oid := in.Source.Address().ObjectID; oid == StatioSysTablesOID || oid == StatioUserTablesOID {
			system := namespace.Name() == "pg_catalog" || namespace.Name() == "information_schema" ||
				strings.HasPrefix(namespace.Name(), "pg_toast")
			if (oid == StatioSysTablesOID && !system) || (oid == StatioUserTablesOID && system) {
				continue
			}
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(address.ObjectID))
		if err != nil {
			return nil, err
		}
		schema := sqltype.String(in.Source.Columns[1].Usage.Type, namespace.Name())
		name := sqltype.String(in.Source.Columns[2].Usage.Type, relation.Name())
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return oid, nil }) ||
			in.Keep.Rejects(1, func() (sqltype.Value, error) { return schema, nil }) ||
			in.Keep.Rejects(2, func() (sqltype.Value, error) { return name, nil }) {
			continue
		}
		heap := in.Runtime.TableBlockStatistics(address)
		var index IndexBlockFact
		for _, child := range indexes[address] {
			fact := in.Runtime.IndexBlockStatistics(child)
			index.Read += fact.Read
			index.Hit += fact.Hit
		}
		row := make([]sqltype.Value, len(statioTableColumns))
		row[0], row[1], row[2] = oid, schema, name
		for i, value := range [...]int64{heap.Read, heap.Hit, index.Read, index.Hit, 0, 0, 0, 0} {
			column := i + 3
			if column >= 7 || ((column == 5 || column == 6) && len(indexes[address]) == 0) {
				row[column] = sqltype.Null(in.Source.Columns[column].Usage.Type)
				continue
			}
			row[column], err = sqltype.Integer(in.Source.Columns[column].Usage.Type, value)
			if err != nil {
				return nil, err
			}
		}
		// No TOAST relation: strict direct functions and empty index SUMs yield NULL.
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
