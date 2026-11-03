package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const StatioAllIndexesOID ids.OID = 12358

// StatioUserIndexesOID shares ALL's producer and transaction statistics Reader.
const StatioUserIndexesOID ids.OID = 12359

type IndexBlockFact struct {
	Read, Hit int64
}

var statioIndexColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"indexrelid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 950, 0, -1},
	{"relname", sqltype.Name, true, 950, 0, -1},
	{"indexrelname", sqltype.Name, true, 950, 0, -1},
	{"idx_blks_read", sqltype.Int8, true, 0, 0, -1},
	{"idx_blks_hit", sqltype.Int8, true, 0, 0, -1},
}

func statioIndexParentKind(kind string) bool {
	switch kind {
	case "r", "t", "m":
		return true
	default:
		return false
	}
}

func captureStatioIndexes(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, statioIndexColumns); err != nil {
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
			continue
		}
		if !statioIndexParentKind(ClassKind(in.Catalog, parent)) {
			continue
		}
		namespace, ok := in.Catalog.Namespace(parent.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing index statistics namespace")
		}
		if in.Source.Address().ObjectID == StatioUserIndexesOID &&
			(namespace.Name() == "pg_catalog" || namespace.Name() == "information_schema" || strings.HasPrefix(namespace.Name(), "pg_toast")) {
			continue
		}
		parentID, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(parent.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		indexID, err := sqltype.OIDValue(in.Source.Columns[1].Usage.Type, uint32(index.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		schemaName := sqltype.String(in.Source.Columns[2].Usage.Type, namespace.Name())
		parentName := sqltype.String(in.Source.Columns[3].Usage.Type, parent.Name())
		indexName := sqltype.String(in.Source.Columns[4].Usage.Type, index.Name())
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return parentID, nil }) ||
			in.Keep.Rejects(1, func() (sqltype.Value, error) { return indexID, nil }) ||
			in.Keep.Rejects(3, func() (sqltype.Value, error) { return parentName, nil }) ||
			in.Keep.Rejects(4, func() (sqltype.Value, error) { return indexName, nil }) {
			continue
		}
		fact := in.Runtime.IndexBlockStatistics(index.Address())
		blocksRead, err := sqltype.Integer(in.Source.Columns[5].Usage.Type, fact.Read)
		if err != nil {
			return nil, err
		}
		blocksHit, err := sqltype.Integer(in.Source.Columns[6].Usage.Type, fact.Hit)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{parentID, indexID, schemaName, parentName, indexName, blocksRead, blocksHit})
	}
	return rows, in.Context.Err()
}
