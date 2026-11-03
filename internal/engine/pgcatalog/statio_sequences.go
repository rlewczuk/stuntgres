package pgcatalog

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatioAllSequencesOID is a native identity, not an initdb-assigned view OID.
const StatioAllSequencesOID ids.OID = 12213

// StatioSysSequencesOID shares its producer and statistics with the all view.
const StatioSysSequencesOID ids.OID = 12218

var statioSequencesColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1},
	{"schemaname", sqltype.Name, true, 0, 0, -1},
	{"relname", sqltype.Name, true, 0, 0, -1},
	{"blks_read", sqltype.Int8, true, 0, 0, -1},
	{"blks_hit", sqltype.Int8, true, 0, 0, -1},
}

func captureStatioSequences(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, statioSequencesColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	systemOnly := in.Source.Address().ObjectID == StatioSysSequencesOID
	for _, descriptor := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok || d.RelationKind != catalog.SequenceRelation {
			continue
		}
		ns, ok := in.Catalog.Namespace(d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing sequence statistics namespace")
		}
		if systemOnly && ns.Name() != "pg_catalog" && ns.Name() != "information_schema" && !strings.HasPrefix(ns.Name(), "pg_toast") {
			continue
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		name := sqltype.String(in.Source.Columns[2].Usage.Type, d.Name())
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return oid, nil }) || in.Keep.Rejects(2, func() (sqltype.Value, error) { return name, nil }) {
			continue
		}
		// One infinitely resident logical page per tracked history. Project
		// after the transaction reader, not while individual writers merge.
		n := in.Runtime.SequenceIOStatistics(d.Address())
		var reads, hits int64
		if n > 0 {
			reads, hits = 1, n-1
		}
		read, err := sqltype.Integer(in.Source.Columns[3].Usage.Type, reads)
		if err != nil {
			return nil, err
		}
		hit, err := sqltype.Integer(in.Source.Columns[4].Usage.Type, hits)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{oid, sqltype.String(in.Source.Columns[1].Usage.Type, ns.Name()), name, read, hit})
	}
	return rows, in.Context.Err()
}
