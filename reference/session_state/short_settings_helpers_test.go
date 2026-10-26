package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type shortSetting struct {
	name, kind, context, boot, show, category, description, extra string
	unit, min, max, enums any
}

// Independent release metadata; 26 short spellings identify these 25 GUCs.
var shortSettings = []shortSetting{
	{name: "DateStyle", kind: "string", context: "user", boot: "ISO, MDY", show: "ISO, MDY", category: "Client Connection Defaults / Locale and Formatting", description: "Sets the display format for date and time values.", extra: "Also controls interpretation of ambiguous date inputs."},
	{name: "allow_system_table_mods", kind: "bool", context: "superuser", boot: "off", show: "off", category: "Developer Options", description: "Allows modifications of the structure of system tables."},
	{name: "enable_bitmapscan", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of bitmap-scan plans."},
	{name: "enable_hashjoin", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of hash join plans."},
	{name: "enable_indexonlyscan", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of index-only-scan plans."},
	{name: "enable_indexscan", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of index-scan plans."},
	{name: "enable_mergejoin", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of merge join plans."},
	{name: "enable_nestloop", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of nested-loop join plans."},
	{name: "enable_seqscan", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of sequential-scan plans."},
	{name: "enable_tidscan", kind: "bool", context: "user", boot: "on", show: "on", category: "Query Tuning / Planner Method Configuration", description: "Enables the planner's use of TID scan plans."},
	{name: "fsync", kind: "bool", context: "sighup", boot: "on", show: "on", category: "Write-Ahead Log / Settings", description: "Forces synchronization of updates to disk.", extra: "The server will use the fsync() system call in several places to make sure that updates are physically written to disk. This ensures that a database cluster will recover to a consistent state after an operating system or hardware crash."},
	{name: "ignore_system_indexes", kind: "bool", context: "backend", boot: "off", show: "off", category: "Developer Options", description: "Disables reading from system indexes.", extra: "It does not prevent updating the indexes, so it is safe to use.  The worst consequence is slowness."},
	{name: "listen_addresses", kind: "string", context: "postmaster", boot: "localhost", show: "localhost", category: "Connections and Authentication / Connection Settings", description: "Sets the host name or IP address(es) to listen to."},
	{name: "log_executor_stats", kind: "bool", context: "superuser", boot: "off", show: "off", category: "Statistics / Monitoring", description: "Writes executor performance statistics to the server log."},
	{name: "log_min_messages", kind: "enum", context: "superuser", boot: "warning", show: "warning", category: "Reporting and Logging / When to Log", description: "Sets the message levels that are logged.", extra: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", enums: "{debug5,debug4,debug3,debug2,debug1,info,notice,warning,error,log,fatal,panic}"},
	{name: "log_parser_stats", kind: "bool", context: "superuser", boot: "off", show: "off", category: "Statistics / Monitoring", description: "Writes parser performance statistics to the server log."},
	{name: "log_planner_stats", kind: "bool", context: "superuser", boot: "off", show: "off", category: "Statistics / Monitoring", description: "Writes planner performance statistics to the server log."},
	{name: "log_statement_stats", kind: "bool", context: "superuser", boot: "off", show: "off", category: "Statistics / Monitoring", description: "Writes cumulative performance statistics to the server log."},
	{name: "max_connections", kind: "integer", context: "postmaster", boot: "100", show: "100", category: "Connections and Authentication / Connection Settings", description: "Sets the maximum number of concurrent connections.", min: "1", max: "262143"},
	{name: "port", kind: "integer", context: "postmaster", boot: "5432", show: "5432", category: "Connections and Authentication / Connection Settings", description: "Sets the TCP port the server listens on.", min: "1", max: "65535"},
	{name: "post_auth_delay", kind: "integer", context: "backend", boot: "0", show: "0", category: "Developer Options", description: "Sets the amount of time to wait after authentication on connection startup.", extra: "This allows attaching a debugger to the process.", unit: "s", min: "0", max: "2147"},
	{name: "shared_buffers", kind: "integer", context: "postmaster", boot: "16384", show: "128MB", category: "Resource Usage / Memory", description: "Sets the number of shared memory buffers used by the server.", unit: "8kB", min: "16", max: "1073741823"},
	{name: "ssl", kind: "bool", context: "sighup", boot: "off", show: "off", category: "Connections and Authentication / SSL", description: "Enables SSL connections."},
	{name: "unix_socket_directories", kind: "string", context: "postmaster", boot: "/tmp", show: "/tmp", category: "Connections and Authentication / Connection Settings", description: "Sets the directories where Unix-domain sockets will be created."},
	{name: "work_mem", kind: "integer", context: "user", boot: "4096", show: "4MB", category: "Resource Usage / Memory", description: "Sets the maximum memory to be used for query workspaces.", extra: "This much memory can be used by each internal sort operation and hash table before switching to temporary disk files.", unit: "kB", min: "64", max: "2147483647"},
}

var shortStages = []string{"log_parser_stats", "log_planner_stats", "log_executor_stats"}
var shortStats = []string{"log_executor_stats", "log_parser_stats", "log_planner_stats", "log_statement_stats"}

func shortMutable(d shortSetting) bool { return d.context == "user" || d.context == "superuser" }
func shortInitial(d shortSetting, pg bool) (show, raw, source, boot string) {
	show, raw, source, boot = d.show, d.boot, "default", d.boot
	if pg {
		switch d.name {
		case "DateStyle": source = "client"
		case "fsync": show, raw, source = "off", "off", "command line"
		case "listen_addresses": show, raw, source = "*", "*", "configuration file"
		case "max_connections", "shared_buffers": source = "configuration file"
		case "unix_socket_directories": show, raw, boot = "/var/run/postgresql", "/var/run/postgresql", "/var/run/postgresql"
		}
	}
	return
}
func shortNames() string {
	names := make([]string, len(shortSettings))
	for i, d := range shortSettings { names[i] = physicalLiteral(d.name) }
	return strings.Join(names, ",")
}
func (s *physicalCase) shortRead(name, show, raw, source string, prepared bool) {
	for _, q := range []struct{ sql, column string; args []any }{
		{"SHOW " + name, name, nil},
		{"SELECT current_setting($1)", "current_setting", []any{strings.ToUpper(name)}},
	} {
		step := fixture.Select(q.sql, []string{q.column}, []string{"TEXT"}, [][]any{{show}}, q.args...)
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, q.column == "current_setting") }
		s.run(step)
	}
	step := fixture.Select("SELECT setting,source FROM pg_settings WHERE name=$1", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{raw, source}}, name)
	step.Prepared = prepared
	s.run(step)
}
func (s *physicalCase) shortMetadata(d shortSetting, prepared bool) {
	_, raw, source, boot := shortInitial(d, s.pg)
	want := []any{d.name, raw, d.unit, d.category, d.description, connectionNullable(d.extra), d.context, d.kind, source, d.min, d.max, d.enums, boot, raw, nil, nil, false}
	query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE name=" + physicalLiteral(d.name)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, query) }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	if !rows.Next() { s.t.Fatal("missing short metadata", d.name, rows.Err()) }
	cells, dest := make([]any, 17), make([]any, 17)
	for i := range cells { dest[i] = &cells[i] }
	if err := rows.Scan(dest...); err != nil { s.t.Fatal(err) }
	for i := range cells { cells[i] = fixture.Normalize(cells[i]) }
	if s.pg && source == "configuration file" {
		file, ok := cells[14].(string); line, valid := cells[15].(int64)
		if !ok || !path.IsAbs(file) || path.Base(file) != "postgresql.conf" || !valid || line <= 0 { s.t.Fatal("configuration provenance", cells) }
		want[14], want[15] = file, line
	}
	if !reflect.DeepEqual(cells, want) { s.t.Fatalf("%s metadata got %#v want %#v", d.name, cells, want) }
	if rows.Next() || rows.Err() != nil { s.t.Fatal("metadata cardinality", rows.Err()) }
}
func (s *physicalCase) shortVisible(allowed, prepared bool) {
	var want [][]any
	for _, d := range shortSettings { if allowed || d.name != "unix_socket_directories" { want = append(want, []any{d.name}) } }
	step := fixture.Select("SELECT name FROM pg_settings WHERE name IN ("+shortNames()+") ORDER BY name", []string{"name"}, []string{"TEXT"}, want)
	step.Prepared = prepared; s.run(step)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL"); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL") }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) { s.t.Fatal(columns, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 { s.t.Fatal(types, err) }
	for _, typ := range types { if typ.DatabaseTypeName() != "TEXT" { s.t.Fatal(typ) } }
	seen := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil { s.t.Fatal(err) }
		for _, d := range shortSettings { if name == d.name {
			show, _, _, _ := shortInitial(d, s.pg)
			if (!allowed && name == "unix_socket_directories") || seen[name] || value != show || description != d.description { s.t.Fatal("SHOW ALL", name, value, description) }
			seen[name] = true
		} }
	}
	if rows.Err() != nil || len(seen) != len(want) { s.t.Fatal("SHOW ALL membership", seen, rows.Err()) }
}
func shortConflict(name string) fixture.Diagnostic {
	d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "%s": 1`, name))
	d.Detail = `Cannot enable parameter when "log_statement_stats" is true.`
	if name == "log_statement_stats" { d.Detail = `Cannot enable "log_statement_stats" when "log_parser_stats", "log_planner_stats", or "log_executor_stats" is true.` }
	return d
}
func (s *physicalCase) shortSnapshot(values, sources []string, prepared bool) {
	want := make([][]any, 4)
	for i, name := range shortStats { want[i] = []any{name, values[i], sources[i]} }
	step := fixture.Select("SELECT name,setting,source FROM pg_settings WHERE name IN ('log_executor_stats','log_parser_stats','log_planner_stats','log_statement_stats') ORDER BY name", []string{"name", "setting", "source"}, []string{"TEXT", "TEXT", "TEXT"}, want)
	step.Prepared = prepared; s.run(step)
	s.shortSentinel()
}
func (s *physicalCase) shortSentinel() {
	s.run(fixture.Select("SELECT v FROM short_sentinel ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(11)}}))
}
func (s *physicalCase) shortCreateSentinel() {
	s.run(fixture.Exec("CREATE TABLE short_sentinel(v integer PRIMARY KEY CHECK(v>0))", 0))
	s.run(fixture.Exec("INSERT INTO short_sentinel VALUES(7),(11)", 2))
}
func (s *physicalCase) shortControls(overrides map[string][2]string) {
	if s.pg { return }
	values := func() {
		for _, d := range shortSettings {
			show, raw, source, _ := shortInitial(d, false)
			if override, ok := overrides[d.name]; ok { show, raw, source = override[0], override[1], "session" }
			s.shortRead(d.name, show, raw, source, true)
		}
	}
	values()
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil { s.t.Fatal(err) }
	for _, d := range shortSettings { if strings.Contains(dump.String(), "SET "+d.name) { s.t.Fatal("Dump serialized live setting", d.name) } }
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil { s.t.Fatal("invalid restore accepted") }
	s.shortSentinel()
	values()
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil { s.t.Fatal(err) }
	s.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='short_sentinel'", []string{"relname"}, []string{"NAME"}, nil))
	values()
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil { s.t.Fatal(err) }
	s.shortSentinel()
	values()
}
