package pgcatalog

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"time"
)

// StatIOOID is the native inventory identity, not an initdb-allocated OID.
const StatIOOID ids.OID = 12301

var statIOColumns = []ColumnSpec{
	{"backend_type", sqltype.Text, true, 100, 0, -1},
	{"object", sqltype.Text, true, 100, 0, -1},
	{"context", sqltype.Text, true, 100, 0, -1},
	{"reads", sqltype.Int8, true, 0, 0, -1},
	{"read_bytes", sqltype.Numeric, true, 0, 0, -1},
	{"read_time", sqltype.Float8, true, 0, 0, -1},
	{"writes", sqltype.Int8, true, 0, 0, -1},
	{"write_bytes", sqltype.Numeric, true, 0, 0, -1},
	{"write_time", sqltype.Float8, true, 0, 0, -1},
	{"writebacks", sqltype.Int8, true, 0, 0, -1},
	{"writeback_time", sqltype.Float8, true, 0, 0, -1},
	{"extends", sqltype.Int8, true, 0, 0, -1},
	{"extend_bytes", sqltype.Numeric, true, 0, 0, -1},
	{"extend_time", sqltype.Float8, true, 0, 0, -1},
	{"hits", sqltype.Int8, true, 0, 0, -1},
	{"evictions", sqltype.Int8, true, 0, 0, -1},
	{"reuses", sqltype.Int8, true, 0, 0, -1},
	{"fsyncs", sqltype.Int8, true, 0, 0, -1},
	{"fsync_time", sqltype.Float8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

// These are capabilities, not claims that embedded workers or IO exist.
// REL_18_6 pgstat_io.c and regress/expected/stats.out include relation/init.
var statIOBackends = [...]struct {
	name     string
	contexts []string
	temp     bool
}{
	{"autovacuum launcher", []string{"bulkread", "init", "normal"}, false},
	{"autovacuum worker", []string{"bulkread", "init", "normal", "vacuum"}, false},
	{"background worker", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, true},
	{"background writer", []string{"init", "normal"}, false},
	{"checkpointer", []string{"init", "normal"}, false},
	{"client backend", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, true},
	{"io worker", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, true},
	{"slotsync worker", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, true},
	{"standalone backend", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, false},
	{"startup", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, false},
	{"walreceiver", nil, false},
	{"walsender", []string{"bulkread", "bulkwrite", "init", "normal", "vacuum"}, true},
	{"walsummarizer", nil, false},
	{"walwriter", nil, false},
}

type statIOOperation uint8

const (
	ioRead statIOOperation = 1 << iota
	ioWrite
	ioWriteback
	ioExtend
	ioHit
	ioEvict
	ioReuse
	ioFsync
)

var statIOFieldOperations = [...]statIOOperation{
	ioRead, ioRead, ioRead, ioWrite, ioWrite, ioWrite,
	ioWriteback, ioWriteback, ioExtend, ioExtend, ioExtend,
	ioHit, ioEvict, ioReuse, ioFsync, ioFsync,
}

func statIOSupport(backend, object, context string) statIOOperation {
	mask := ioRead | ioWrite | ioWriteback | ioExtend | ioHit | ioEvict | ioReuse | ioFsync
	switch backend {
	case "background writer":
		mask &^= ioRead | ioHit | ioEvict | ioExtend
	case "checkpointer":
		mask &^= ioHit | ioEvict | ioExtend
		if object != "wal" {
			mask &^= ioRead
		}
	case "autovacuum launcher":
		mask &^= ioExtend
	}
	if object == "wal" {
		switch backend {
		case "autovacuum launcher", "autovacuum worker", "background writer", "walreceiver", "walwriter":
			mask &^= ioRead
		}
		if context == "init" {
			mask &= ioWrite | ioFsync
		} else {
			mask &= ioRead | ioWrite | ioFsync
		}
	}
	if object == "temp relation" {
		mask &^= ioFsync | ioWriteback
	}
	if context == "bulkread" {
		mask &^= ioExtend
	}
	switch context {
	case "bulkread", "bulkwrite", "vacuum":
		mask &^= ioFsync
	default:
		mask &^= ioReuse
	}
	return mask
}

func captureStatIO(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, statIOColumns); err != nil {
		return nil, err
	}
	var types [20]*sqltype.Descriptor
	for i := range types {
		types[i] = in.Source.Columns[i].Usage.Type
	}
	return PhysicalIORows(in.Context, types, "", in.Runtime.IOStatisticsReset())
}

// PhysicalIORows shares the capability matrix with the view. Empty backend
// selects the whole view; a getter supplies one genuinely present backend kind
// and its own backend reset, never the shared IO reset.
func PhysicalIORows(ctx context.Context, types [20]*sqltype.Descriptor, selected string, reset time.Time) ([][]sqltype.Value, error) {
	// Absent physical PostgreSQL IO: counts = |empty event set|, bytes and
	// milliseconds = sums over that set. Never reinterpret logical DML/sequence
	// encounters or unrelated keyed provider facts as physical IO.
	integer, err := sqltype.Integer(types[3], 0)
	if err != nil {
		return nil, err
	}
	numeric, err := sqltype.NumericUint64Value(types[4], 0)
	if err != nil {
		return nil, err
	}
	floating, err := sqltype.Float(types[5], 0)
	if err != nil {
		return nil, err
	}
	var template [20]sqltype.Value
	for i := 3; i < 19; i++ {
		switch statIOColumns[i].Type {
		case sqltype.Int8:
			template[i] = integer
		case sqltype.Numeric:
			template[i] = numeric
		case sqltype.Float8:
			template[i] = floating
		}
	}
	template[19] = sqltype.Null(types[19])
	if !reset.IsZero() {
		template[19], err = sqltype.TimestampFromTime(types[19], reset.UTC())
		if err != nil {
			return nil, err
		}
	}
	capacity := 8
	if selected == "" {
		capacity = 79
	}
	rows := make([][]sqltype.Value, 0, capacity)
	appendRow := func(backend sqltype.Value, name, object, context string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := make([]sqltype.Value, len(template))
		copy(row, template[:])
		row[0] = backend
		row[1] = sqltype.String(types[1], object)
		row[2] = sqltype.String(types[2], context)
		mask := statIOSupport(name, object, context)
		for i, operation := range statIOFieldOperations {
			if mask&operation == 0 {
				row[i+3] = sqltype.Null(types[i+3])
			}
		}
		rows = append(rows, row)
		return nil
	}
	for _, backend := range statIOBackends {
		if selected != "" && selected != backend.name {
			continue
		}
		name := sqltype.String(types[0], backend.name)
		for _, context := range backend.contexts {
			if err := appendRow(name, backend.name, "relation", context); err != nil {
				return nil, err
			}
		}
		if backend.temp {
			if err := appendRow(name, backend.name, "temp relation", "normal"); err != nil {
				return nil, err
			}
		}
		for _, context := range [...]string{"init", "normal"} {
			if err := appendRow(name, backend.name, "wal", context); err != nil {
				return nil, err
			}
		}
	}
	return rows, ctx.Err()
}
