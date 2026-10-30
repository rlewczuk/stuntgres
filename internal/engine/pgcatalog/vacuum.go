package pgcatalog

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"time"
)

// Native source identities follow the existing 120xx relation convention.
// Released system_views CREATE VIEW OIDs are initdb-generated, not fixed IDs.
const (
	StatAllTablesOID  ids.OID = 12009
	StatSysTablesOID  ids.OID = 12010
	StatUserTablesOID ids.OID = 12011
	ProgressVacuumOID ids.OID = 12012
)

type MaintenanceFact struct {
	FrozenXID                                              uint64
	Scanned                                                bool
	Live, Dead                                             int64
	Tuples                                                 int64
	Inserts, Updates, Deletes, Modifications, InsertsSince int64
	VacuumCount                                            int64
	LastVacuum                                             time.Time
	AnalyzeCount                                           int64
	LastAnalyze                                            time.Time
	InheritedScanned                                       bool
	InheritedTuples                                        int64
	ForeignAnalyzed                                        bool
	ForeignRows, ForeignDeadRows, ForeignPages             float64
}
type VacuumProgressFact struct {
	PID                                                                 int64
	Database                                                            ids.OID
	DatabaseName                                                        string
	Relation                                                            ids.OID
	Phase                                                               string
	HeapBlocksTotal, HeapBlocksScanned, HeapBlocksVacuumed              int64
	IndexVacuumCount, MaxDeadTupleBytes, DeadTupleBytes, NumDeadItemIDs int64
	IndexesTotal, IndexesProcessed                                      int64
	Visible                                                             bool
}

// ProgressViewer decides which active progress rows the current user reads
// in full: superuser, inherited worker authenticated role or pg_read_all_stats.
type ProgressViewer struct {
	superuser  bool
	principals []authorization.PrincipalRef
	reader     authorization.PrincipalRef
	hasReader  bool
}

// NewProgressViewer captures current's inherited role closure in roles.
func NewProgressViewer(ctx context.Context, roles authorization.Read, current authorization.PrincipalRef) (ProgressViewer, error) {
	principals, err := roles.RoleClosure(ctx, current, true)
	if err != nil {
		return ProgressViewer{}, err
	}
	v := ProgressViewer{principals: principals}
	reader, hasReader := roles.Lookup("pg_read_all_stats")
	v.reader, v.hasReader = reader.Ref, hasReader
	actor, hasActor := roles.Find(current)
	v.superuser = hasActor && actor.Superuser
	return v, nil
}

// Visible reports whether the viewer reads the row of a worker authenticated
// as owner.
func (v ProgressViewer) Visible(owner authorization.PrincipalRef) bool {
	visible := v.superuser
	for _, principal := range v.principals {
		if principal == owner || v.hasReader && principal == v.reader {
			visible = true
		}
	}
	return visible
}

var tableStatisticsColumns = []ColumnSpec{
	{"relid", sqltype.Oid, true, 0, 0, -1}, {"schemaname", sqltype.Name, true, 0, 0, -1}, {"relname", sqltype.Name, true, 0, 0, -1},
	{"seq_scan", sqltype.Int8, true, 0, 0, -1}, {"last_seq_scan", sqltype.Timestamptz, true, 0, 0, -1}, {"seq_tup_read", sqltype.Int8, true, 0, 0, -1},
	{"idx_scan", sqltype.Int8, true, 0, 0, -1}, {"last_idx_scan", sqltype.Timestamptz, true, 0, 0, -1}, {"idx_tup_fetch", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_ins", sqltype.Int8, true, 0, 0, -1}, {"n_tup_upd", sqltype.Int8, true, 0, 0, -1}, {"n_tup_del", sqltype.Int8, true, 0, 0, -1},
	{"n_tup_hot_upd", sqltype.Int8, true, 0, 0, -1}, {"n_tup_newpage_upd", sqltype.Int8, true, 0, 0, -1}, {"n_live_tup", sqltype.Int8, true, 0, 0, -1},
	{"n_dead_tup", sqltype.Int8, true, 0, 0, -1}, {"n_mod_since_analyze", sqltype.Int8, true, 0, 0, -1}, {"n_ins_since_vacuum", sqltype.Int8, true, 0, 0, -1},
	{"last_vacuum", sqltype.Timestamptz, true, 0, 0, -1}, {"last_autovacuum", sqltype.Timestamptz, true, 0, 0, -1}, {"last_analyze", sqltype.Timestamptz, true, 0, 0, -1}, {"last_autoanalyze", sqltype.Timestamptz, true, 0, 0, -1},
	{"vacuum_count", sqltype.Int8, true, 0, 0, -1}, {"autovacuum_count", sqltype.Int8, true, 0, 0, -1}, {"analyze_count", sqltype.Int8, true, 0, 0, -1}, {"autoanalyze_count", sqltype.Int8, true, 0, 0, -1},
	{"total_vacuum_time", sqltype.Float8, true, 0, 0, -1}, {"total_autovacuum_time", sqltype.Float8, true, 0, 0, -1}, {"total_analyze_time", sqltype.Float8, true, 0, 0, -1}, {"total_autoanalyze_time", sqltype.Float8, true, 0, 0, -1},
}
var progressVacuumColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1}, {"datid", sqltype.Oid, true, 0, 0, -1}, {"datname", sqltype.Name, true, 0, 0, -1}, {"relid", sqltype.Oid, true, 0, 0, -1}, {"phase", sqltype.Text, true, 950, 0, -1},
	{"heap_blks_total", sqltype.Int8, true, 0, 0, -1}, {"heap_blks_scanned", sqltype.Int8, true, 0, 0, -1}, {"heap_blks_vacuumed", sqltype.Int8, true, 0, 0, -1},
	{"index_vacuum_count", sqltype.Int8, true, 0, 0, -1}, {"max_dead_tuple_bytes", sqltype.Int8, true, 0, 0, -1}, {"dead_tuple_bytes", sqltype.Int8, true, 0, 0, -1}, {"num_dead_item_ids", sqltype.Int8, true, 0, 0, -1},
	{"indexes_total", sqltype.Int8, true, 0, 0, -1}, {"indexes_processed", sqltype.Int8, true, 0, 0, -1}, {"delay_time", sqltype.Float8, true, 0, 0, -1},
}

func validateMaintenanceSource(in Input, columns []ColumnSpec) error {
	if err := in.Context.Err(); err != nil {
		return err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Source == nil || in.Source.Address() != in.Ref.Address || in.Catalog.Get(in.Ref.Address) != in.Source || len(in.Source.Columns) != len(columns) || in.Runtime == nil {
		return fmt.Errorf("stuntgres: invalid maintenance source capture")
	}
	for i, column := range columns {
		owned, ok := in.Catalog.LookupType(column.Type)
		if !ok || owned != in.Source.Columns[i].Usage.Type || in.Source.Columns[i].Name != column.Name {
			return fmt.Errorf("stuntgres: invalid maintenance source shape")
		}
	}
	return nil
}
func captureTableStatistics(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, tableStatisticsColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	var descriptors []*catalog.Relation
	for _, descriptor := range in.Catalog.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok {
			kind := ClassKind(in.Catalog, d)
			if kind == "r" || kind == "t" || kind == "m" || kind == "p" {
				descriptors = append(descriptors, d)
			}
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Address().ObjectID < descriptors[j].Address().ObjectID })
	for _, d := range descriptors {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		ns, ok := in.Catalog.Namespace(d.Namespace())
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing statistics namespace")
		}
		system := ns.Name() == "pg_catalog" || ns.Name() == "information_schema" || ns.TemporaryToast
		if in.Ref.Address.ObjectID == StatSysTablesOID && !system || in.Ref.Address.ObjectID == StatUserTablesOID && system {
			continue
		}
		row := make([]sqltype.Value, len(tableStatisticsColumns))
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		integer := func(i int, n int64) error {
			v, err := sqltype.Integer(in.Source.Columns[i].Usage.Type, n)
			row[i] = v
			return err
		}
		oid, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[0] = oid
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, ns.Name())
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, d.Name())
		fact, _ := in.Runtime.Maintenance(d.Address())
		counters := [...]struct {
			column int
			value  int64
		}{{9, fact.Inserts}, {10, fact.Updates}, {11, fact.Deletes}, {12, 0}, {13, 0}, {14, fact.Live}, {15, fact.Dead}, {16, fact.Modifications}, {17, fact.InsertsSince}, {22, fact.VacuumCount}, {23, 0}, {24, fact.AnalyzeCount}, {25, 0}}
		for _, counter := range counters {
			if err := integer(counter.column, counter.value); err != nil {
				return nil, err
			}
		}
		if !fact.LastVacuum.IsZero() {
			row[18], err = sqltype.TimestampFromTime(in.Source.Columns[18].Usage.Type, fact.LastVacuum)
			if err != nil {
				return nil, err
			}
		}
		if !fact.LastAnalyze.IsZero() {
			row[20], err = sqltype.TimestampFromTime(in.Source.Columns[20].Usage.Type, fact.LastAnalyze)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
func captureVacuumProgress(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, progressVacuumColumns); err != nil {
		return nil, err
	}
	// A nonnil empty result is an actual anonymous query source, not a
	// missing retained data view. Match other dynamic catalog producers.
	rows := make([][]sqltype.Value, 0)
	err := in.Runtime.VisitVacuumProgress(in.Context, func(f VacuumProgressFact) error {
		if err := in.Context.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(progressVacuumColumns))
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
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, f.DatabaseName)
		if f.Visible {
			row[3], err = sqltype.OIDValue(in.Source.Columns[3].Usage.Type, uint32(f.Relation))
			if err != nil {
				return err
			}
			row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, f.Phase)
			values := [...]int64{f.HeapBlocksTotal, f.HeapBlocksScanned, f.HeapBlocksVacuumed, f.IndexVacuumCount, f.MaxDeadTupleBytes, f.DeadTupleBytes, f.NumDeadItemIDs, f.IndexesTotal, f.IndexesProcessed}
			for i, value := range values {
				row[i+5], err = sqltype.Integer(in.Source.Columns[i+5].Usage.Type, value)
				if err != nil {
					return err
				}
			}
			row[14], err = sqltype.Float(in.Source.Columns[14].Usage.Type, 0)
			if err != nil {
				return err
			}
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}
