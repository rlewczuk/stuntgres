package pgcatalog

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"time"
)

// Native relation identity; PostgreSQL's system-view OID is initdb-generated.
const StatDatabaseOID ids.OID = 12212
const StatDatabaseRuleOID ids.OID = 12360

type DatabaseStatisticsFact struct {
	Cumulative statistics.DatabaseFact
	Backends   int64
	Checksums  bool
}

var databaseStatisticsColumns = []ColumnSpec{
	{"datid", sqltype.Oid, true, 0, 0, -1},
	{"datname", sqltype.Name, true, 0, 0, -1},
	{"numbackends", sqltype.Int4, true, 0, 0, -1},
	{"xact_commit", sqltype.Int8, true, 0, 0, -1},
	{"xact_rollback", sqltype.Int8, true, 0, 0, -1},
	{"blks_read", sqltype.Int8, true, 0, 0, -1},
	{"blks_hit", sqltype.Int8, true, 0, 0, -1},
	{"tup_returned", sqltype.Int8, true, 0, 0, -1},
	{"tup_fetched", sqltype.Int8, true, 0, 0, -1},
	{"tup_inserted", sqltype.Int8, true, 0, 0, -1},
	{"tup_updated", sqltype.Int8, true, 0, 0, -1},
	{"tup_deleted", sqltype.Int8, true, 0, 0, -1},
	{"conflicts", sqltype.Int8, true, 0, 0, -1},
	{"temp_files", sqltype.Int8, true, 0, 0, -1},
	{"temp_bytes", sqltype.Int8, true, 0, 0, -1},
	{"deadlocks", sqltype.Int8, true, 0, 0, -1},
	{"checksum_failures", sqltype.Int8, true, 0, 0, -1},
	{"checksum_last_failure", sqltype.Timestamptz, true, 0, 0, -1},
	{"blk_read_time", sqltype.Float8, true, 0, 0, -1},
	{"blk_write_time", sqltype.Float8, true, 0, 0, -1},
	{"session_time", sqltype.Float8, true, 0, 0, -1},
	{"active_time", sqltype.Float8, true, 0, 0, -1},
	{"idle_in_transaction_time", sqltype.Float8, true, 0, 0, -1},
	{"sessions", sqltype.Int8, true, 0, 0, -1},
	{"sessions_abandoned", sqltype.Int8, true, 0, 0, -1},
	{"sessions_fatal", sqltype.Int8, true, 0, 0, -1},
	{"sessions_killed", sqltype.Int8, true, 0, 0, -1},
	{"parallel_workers_to_launch", sqltype.Int8, true, 0, 0, -1},
	{"parallel_workers_launched", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func captureDatabaseStatistics(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, databaseStatisticsColumns); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	appendDatabase := func(oid ids.OID, name string) error {
		row := make([]sqltype.Value, len(databaseStatisticsColumns))
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		value, err := sqltype.OIDValue(in.Source.Columns[0].Usage.Type, uint32(oid))
		if err != nil {
			return err
		}
		row[0] = value
		if oid != 0 {
			row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, name)
		}
		if in.Keep.Rejects(0, func() (sqltype.Value, error) { return row[0], nil }) || in.Keep.Rejects(1, func() (sqltype.Value, error) { return row[1], nil }) {
			return nil
		}
		f := in.Runtime.DatabaseStatistics(oid)
		d := f.Cumulative
		counters := [...]int64{f.Backends, d.Commits, d.Rollbacks, d.BlocksRead, d.BlocksHit, d.Returned, d.Fetched, d.Inserts, d.Updates, d.Deletes, d.Conflicts, d.TempFiles, d.TempBytes, d.Deadlocks}
		for i, n := range counters {
			row[i+2], err = sqltype.Integer(in.Source.Columns[i+2].Usage.Type, n)
			if err != nil {
				return err
			}
		}
		if f.Checksums {
			row[16], err = sqltype.Integer(in.Source.Columns[16].Usage.Type, d.ChecksumFailures)
			if err != nil {
				return err
			}
			if !d.ChecksumLastFailure.IsZero() {
				row[17], err = sqltype.TimestampFromTime(in.Source.Columns[17].Usage.Type, d.ChecksumLastFailure)
				if err != nil {
					return err
				}
			}
		}
		durations := [...]time.Duration{d.ReadTime, d.WriteTime, d.SessionTime, d.ActiveTime, d.IdleTransactionTime}
		for i, n := range durations {
			row[i+18], err = sqltype.Float(in.Source.Columns[i+18].Usage.Type, float64(n)/float64(time.Millisecond))
			if err != nil {
				return err
			}
		}
		lifecycle := [...]int64{d.Sessions, d.Abandoned, d.Fatal, d.Killed, d.WorkersPlanned, d.WorkersLaunched}
		for i, n := range lifecycle {
			row[i+23], err = sqltype.Integer(in.Source.Columns[i+23].Usage.Type, n)
			if err != nil {
				return err
			}
		}
		if !d.ResetAt.IsZero() {
			row[29], err = sqltype.TimestampFromTime(in.Source.Columns[29].Usage.Type, d.ResetAt)
			if err != nil {
				return err
			}
		}
		rows = append(rows, row)
		return in.Context.Err()
	}
	// Shared-object aggregates have no database name and the released view
	// explicitly returns numbackends=0 (despite the manual's NULL description).
	if err := appendDatabase(0, ""); err != nil {
		return nil, err
	}
	for _, descriptor := range in.Catalog.Descriptors() {
		if database, ok := descriptor.(*catalog.Database); ok {
			if err := appendDatabase(database.Address().ObjectID, database.Name()); err != nil {
				return nil, err
			}
		}
	}
	return rows, in.Context.Err()
}

// REL_18_6 pg_get_viewdef presentation of system_views.sql: the single outer
// FROM item needs no d. qualification; the UNION arm retains pg_database.
// Absent released getters stay symbolic display names, not routine installs.
func databaseStatisticsViewParts(pretty bool) []builtinViewPart {
	var parts []builtinViewPart
	text := func(s string) { parts = append(parts, viewText(s)) }
	ident := func(s string) { parts = append(parts, viewIdentifier(s)) }
	field := func(alias, name string) { ident(alias); text("."); ident(name) }
	getter := func(name string) {
		parts = append(parts, viewRoutine(name, "26"))
		text("(")
		ident("oid")
		text(")")
	}
	text(" SELECT ")
	ident("oid")
	text(" AS ")
	ident("datid")
	text(",\n    ")
	ident("datname")
	text(",\n        CASE\n            WHEN ")
	if !pretty {
		text("(")
	}
	ident("oid")
	text(" = ")
	if !pretty {
		text("(")
	}
	text("0")
	if !pretty {
		text(")")
	}
	text("::")
	parts = append(parts, viewType("oid"))
	if !pretty {
		text(")")
	}
	text(" THEN 0\n            ELSE ")
	getter("pg_stat_get_db_numbackends")
	text("\n        END AS ")
	ident("numbackends")
	for _, column := range []struct{ name, routine string }{
		{"xact_commit", "pg_stat_get_db_xact_commit"},
		{"xact_rollback", "pg_stat_get_db_xact_rollback"},
		{"blks_read", ""},
		{"blks_hit", "pg_stat_get_db_blocks_hit"},
		{"tup_returned", "pg_stat_get_db_tuples_returned"},
		{"tup_fetched", "pg_stat_get_db_tuples_fetched"},
		{"tup_inserted", "pg_stat_get_db_tuples_inserted"},
		{"tup_updated", "pg_stat_get_db_tuples_updated"},
		{"tup_deleted", "pg_stat_get_db_tuples_deleted"},
		{"conflicts", "pg_stat_get_db_conflict_all"},
		{"temp_files", "pg_stat_get_db_temp_files"},
		{"temp_bytes", "pg_stat_get_db_temp_bytes"},
		{"deadlocks", "pg_stat_get_db_deadlocks"},
		{"checksum_failures", "pg_stat_get_db_checksum_failures"},
		{"checksum_last_failure", "pg_stat_get_db_checksum_last_failure"},
		{"blk_read_time", "pg_stat_get_db_blk_read_time"},
		{"blk_write_time", "pg_stat_get_db_blk_write_time"},
		{"session_time", "pg_stat_get_db_session_time"},
		{"active_time", "pg_stat_get_db_active_time"},
		{"idle_in_transaction_time", "pg_stat_get_db_idle_in_transaction_time"},
		{"sessions", "pg_stat_get_db_sessions"},
		{"sessions_abandoned", "pg_stat_get_db_sessions_abandoned"},
		{"sessions_fatal", "pg_stat_get_db_sessions_fatal"},
		{"sessions_killed", "pg_stat_get_db_sessions_killed"},
		{"parallel_workers_to_launch", "pg_stat_get_db_parallel_workers_to_launch"},
		{"parallel_workers_launched", "pg_stat_get_db_parallel_workers_launched"},
		{"stats_reset", "pg_stat_get_db_stat_reset_time"},
	} {
		text(",\n    ")
		if column.routine == "" {
			if !pretty {
				text("(")
			}
			getter("pg_stat_get_db_blocks_fetched")
			text(" - ")
			getter("pg_stat_get_db_blocks_hit")
			if !pretty {
				text(")")
			}
		} else {
			getter(column.routine)
		}
		text(" AS ")
		ident(column.name)
	}
	text("\n   FROM ( SELECT 0 AS ")
	ident("oid")
	text(",\n            NULL::")
	parts = append(parts, viewType("name"))
	text(" AS ")
	ident("datname")
	text("\n        UNION ALL\n         SELECT ")
	field("pg_database", "oid")
	text(",\n            ")
	field("pg_database", "datname")
	text("\n           FROM ")
	parts = append(parts, viewClass("pg_database"))
	text(") ")
	ident("d")
	text(";")
	return parts
}
