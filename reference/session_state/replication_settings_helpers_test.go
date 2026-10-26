package reference_test

import (
 "database/sql"
 "math"
 "reflect"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

type replicationExpectation struct {
 name, kind, unit, raw, initial, min, max, context, category, description, extra string
}

// Independent REL_18_6 English metadata and base-unit defaults.
var replicationSettings = []replicationExpectation{
	{"hot_standby", "bool", "", "on", "on", "", "", "postmaster", "Replication / Standby Servers", "Allows connections and queries during recovery.", ""},
	{"hot_standby_feedback", "bool", "", "off", "off", "", "", "sighup", "Replication / Standby Servers", "Allows feedback from a hot standby to the primary that will avoid query conflicts.", ""},
	{"idle_replication_slot_timeout", "integer", "s", "0", "0", "0", "2147483647", "sighup", "Replication / Sending Servers", "Sets the duration a replication slot can remain idle before it is invalidated.", ""},
	{"max_active_replication_origins", "integer", "", "10", "10", "0", "262143", "postmaster", "Replication / Subscribers", "Sets the maximum number of active replication origins.", ""},
	{"max_logical_replication_workers", "integer", "", "4", "4", "0", "262143", "postmaster", "Replication / Subscribers", "Maximum number of logical replication worker processes.", ""},
	{"max_parallel_apply_workers_per_subscription", "integer", "", "2", "2", "0", "1024", "sighup", "Replication / Subscribers", "Maximum number of parallel apply workers per subscription.", ""},
	{"max_replication_slots", "integer", "", "10", "10", "0", "262143", "postmaster", "Replication / Sending Servers", "Sets the maximum number of simultaneously defined replication slots.", ""},
	{"max_slot_wal_keep_size", "integer", "MB", "-1", "-1", "-1", "2147483647", "sighup", "Replication / Sending Servers", "Sets the maximum WAL size that can be reserved by replication slots.", "Replication slots will be marked as failed, and segments released for deletion or recycling, if this much space is occupied by WAL on disk. -1 means no maximum."},
	{"max_standby_archive_delay", "integer", "ms", "30000", "30s", "-1", "2147483647", "sighup", "Replication / Standby Servers", "Sets the maximum delay before canceling queries when a hot standby server is processing archived WAL data.", "-1 means wait forever."},
	{"max_standby_streaming_delay", "integer", "ms", "30000", "30s", "-1", "2147483647", "sighup", "Replication / Standby Servers", "Sets the maximum delay before canceling queries when a hot standby server is processing streamed WAL data.", "-1 means wait forever."},
	{"max_sync_workers_per_subscription", "integer", "", "2", "2", "0", "262143", "sighup", "Replication / Subscribers", "Maximum number of table synchronization workers per subscription.", ""},
	{"max_wal_senders", "integer", "", "10", "10", "0", "262143", "postmaster", "Replication / Sending Servers", "Sets the maximum number of simultaneously running WAL sender processes.", ""},
	{"output_plugin_libraries", "string", "", "pgoutput, test_decoding", "pgoutput, test_decoding", "", "", "superuser", "Replication / Sending Servers", "Lists libraries that may be named as logical decoding output plugins.", "Users with REPLICATION privileges may only use plugins in this list when creating logical replication slots."},
	{"primary_conninfo", "string", "", "", "", "", "", "sighup", "Replication / Standby Servers", "Sets the connection string to be used to connect to the sending server.", ""},
	{"primary_slot_name", "string", "", "", "", "", "", "sighup", "Replication / Standby Servers", "Sets the name of the replication slot to use on the sending server.", ""},
	{"recovery_min_apply_delay", "integer", "ms", "0", "0", "0", "2147483647", "sighup", "Replication / Standby Servers", "Sets the minimum delay for applying changes during recovery.", ""},
	{"sync_replication_slots", "bool", "", "off", "off", "", "", "sighup", "Replication / Standby Servers", "Enables a physical standby to synchronize logical failover replication slots from the primary server.", ""},
	{"synchronized_standby_slots", "string", "", "", "", "", "", "sighup", "Replication / Primary Server", "Lists streaming replication standby server replication slot names that logical WAL sender processes will wait for.", "Logical WAL sender processes will send decoded changes to output plugins only after the specified replication slots have confirmed receiving WAL."},
	{"synchronous_standby_names", "string", "", "", "", "", "", "sighup", "Replication / Primary Server", "Number of synchronous standbys and list of names of potential synchronous ones.", ""},
	{"track_commit_timestamp", "bool", "", "off", "off", "", "", "postmaster", "Replication / Sending Servers", "Collects transaction commit time.", ""},
	{"wal_keep_size", "integer", "MB", "0", "0", "0", "2147483647", "sighup", "Replication / Sending Servers", "Sets the size of WAL files held for standby servers.", ""},
	{"wal_receiver_create_temp_slot", "bool", "", "off", "off", "", "", "sighup", "Replication / Standby Servers", "Sets whether a WAL receiver should create a temporary replication slot if no permanent slot is configured.", ""},
	{"wal_receiver_status_interval", "integer", "s", "10", "10s", "0", "2147483", "sighup", "Replication / Standby Servers", "Sets the maximum interval between WAL receiver status reports to the sending server.", ""},
	{"wal_receiver_timeout", "integer", "ms", "60000", "1min", "0", "2147483647", "sighup", "Replication / Standby Servers", "Sets the maximum wait time to receive data from the sending server.", "0 disables the timeout."},
	{"wal_retrieve_retry_interval", "integer", "ms", "5000", "5s", "1", "2147483647", "sighup", "Replication / Standby Servers", "Sets the time to wait before retrying to retrieve WAL after a failed attempt.", ""},
	{"wal_sender_timeout", "integer", "ms", "60000", "1min", "0", "2147483647", "user", "Replication / Sending Servers", "Sets the maximum time to wait for WAL replication.", ""},
}

func replicationNames() string {
 names := make([]string, 0, len(replicationSettings))
 for _, d := range replicationSettings { names = append(names, physicalLiteral(d.name)) }
 return strings.Join(names, ",")
}
func replicationRestricted(name string) bool { return name == "primary_conninfo" || name == "output_plugin_libraries" }
func replicationNullable(value string) any { if value == "" { return nil }; return value }
func (s *physicalCase) replicationInventory(allowed, prepared bool, overrides map[string]string) {
 s.t.Helper()
 var expected [][]any
 for _, d := range replicationSettings {
  if !allowed && replicationRestricted(d.name) { continue }
  raw, source := d.raw, "default"
  if d.name == "track_commit_timestamp" && s.pg { source = "command line" }
  if v, ok := overrides[d.name]; ok { raw, source = v, "session" }
  expected = append(expected, []any{d.name, raw, replicationNullable(d.unit), d.category, d.description, replicationNullable(d.extra), d.context, d.kind, source, replicationNullable(d.min), replicationNullable(d.max), nil, d.raw, d.raw, nil, nil, false})
 }
 step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name IN ("+replicationNames()+") ORDER BY name", physicalColumns, physicalTypes, expected)
 step.Prepared = prepared
 step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes) }
 s.run(step)
}
func (s *physicalCase) replicationReads(allowed, prepared bool) {
 s.t.Helper()
 for _, d := range replicationSettings {
  for _, query := range []string{"SHOW "+d.name, "SELECT current_setting("+physicalLiteral(d.name)+")", "SELECT current_setting("+physicalLiteral(d.name)+",true)"} {
   show := strings.HasPrefix(query, "SHOW ")
   if !allowed && replicationRestricted(d.name) {
    step := physicalError(query, physicalDenied(d.name), prepared, true)
    if prepared && show { step.ErrorStage = "prepare" }
    s.run(step)
   } else {
    column := "current_setting"; if show { column = d.name }
    step := fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{d.initial}})
    step.Prepared = prepared
    step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, !show) }
    s.run(step)
   }
  }
 }
 s.replicationInventory(allowed, prepared, nil)
 var rows *sql.Rows
 var err error
 if prepared {
  stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL"); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
  rows, err = stmt.QueryContext(s.p.Ctx)
 } else { rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL") }
 if err != nil { s.t.Fatal(err) }; defer rows.Close()
 replicationShowAllMetadata(s.t, rows, s.pg)
 found := map[string]bool{}
 for rows.Next() {
  var name, value, description string
  if err := rows.Scan(&name,&value,&description); err != nil { s.t.Fatal(err) }
  for _, d := range replicationSettings { if name == d.name {
   if found[name] || (!allowed && replicationRestricted(name)) || value != d.initial || description != d.description { s.t.Fatal("unexpected replication SHOW ALL row",name,value,description) }
   found[name] = true
  } }
 }
 count := 24; if allowed { count = 26 }
 if rows.Err() != nil || len(found) != count { s.t.Fatal("replication SHOW ALL", len(found),count,rows.Err()) }
}
func replicationContext(d replicationExpectation) fixture.Diagnostic {
 if d.context == "postmaster" { return physicalRestart(d.name) }
 return physicalDiagnostic("55P02", "parameter \""+d.name+"\" cannot be changed now")
}
func (s *physicalCase) replicationValue(name, show, raw, source string, prepared bool) {
 s.t.Helper()
 for _, query := range []string{"SHOW "+name,"SELECT current_setting('"+name+"')"} {
  column := "current_setting"; if strings.HasPrefix(query,"SHOW ") { column = name }
  step := fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{show}}); step.Prepared = prepared; s.run(step)
 }
 boot := "60000"; if name == "output_plugin_libraries" { boot = "pgoutput, test_decoding" }
 step := fixture.Select("SELECT setting,boot_val,reset_val,source FROM pg_settings WHERE name='"+name+"'", []string{"setting","boot_val","reset_val","source"}, []string{"TEXT","TEXT","TEXT","TEXT"}, [][]any{{raw,boot,boot,source}})
 step.Prepared = prepared; s.run(step)
}

func replicationShowAllMetadata(t *testing.T, rows *sql.Rows, pg bool) {
 t.Helper()
 columns, err := rows.Columns()
 if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) { t.Fatal(columns, err) }
 types, err := rows.ColumnTypes()
 if err != nil || len(types) != 3 { t.Fatal(types, err) }
 for i, typ := range types {
  nullable, known := typ.Nullable()
  length, lengthKnown := typ.Length()
  precision, scale, decimalKnown := typ.DecimalSize()
  if typ.Name() != columns[i] || typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() || nullable || known == pg || length != math.MaxInt64 || !lengthKnown || precision != 0 || scale != 0 || decimalKnown { t.Fatal("SHOW ALL metadata", typ) }
 }
}
