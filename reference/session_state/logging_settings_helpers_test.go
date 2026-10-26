package reference_test

import (
	"database/sql"
	"fmt"
	"path"
	"reflect"
	"strings"

	"stuntgres/reference/fixture"
)

type loggingSetting struct {
	name, kind, context, category, initial, boot, alternate, show, raw, description, extra string
	min, max, unit, enums                                                                  any
}

// Literal release domains and English metadata from REL_18_6 guc_tables.c;
// these expectations neither import nor inspect native definitions.
var loggingSettings = []loggingSetting{
	{name: "application_name", kind: "string", context: "user", initial: "", boot: "", alternate: "logging_probe", show: "logging_probe", raw: "logging_probe", category: "Reporting and Logging / What to Log", description: "Sets the application name to be reported in statistics and logs."},
	{name: "cluster_name", kind: "string", context: "postmaster", initial: "", boot: "", alternate: "probe", show: "probe", raw: "probe", category: "Reporting and Logging / Process Title", description: "Sets the name of the cluster, which is included in the process title."},
	{name: "debug_pretty_print", kind: "bool", context: "user", initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / What to Log", description: "Indents parse and plan tree displays."},
	{name: "debug_print_parse", kind: "bool", context: "user", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs each query's parse tree."},
	{name: "event_source", kind: "string", context: "postmaster", initial: "PostgreSQL", boot: "PostgreSQL", alternate: "probe", show: "probe", raw: "probe", category: "Reporting and Logging / Where to Log", description: "Sets the application name used to identify PostgreSQL messages in the event log."},
	{name: "log_autovacuum_min_duration", kind: "integer", context: "sighup", initial: "10min", boot: "600000", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / What to Log", description: "Sets the minimum execution time above which autovacuum actions will be logged.", extra: "-1 disables logging autovacuum actions. 0 means log all autovacuum actions.", min: "-1", max: "2147483647", unit: "ms"},
	{name: "log_checkpoints", kind: "bool", context: "sighup", initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / What to Log", description: "Logs each checkpoint."},
	{name: "log_connections", kind: "string", context: "superuser-backend", initial: "", boot: "", alternate: "receipt, authentication", show: "receipt, authentication", raw: "receipt, authentication", category: "Reporting and Logging / What to Log", description: "Logs specified aspects of connection establishment and setup."},
	{name: "log_destination", kind: "string", context: "sighup", initial: "stderr", boot: "stderr", alternate: "csvlog,jsonlog", show: "csvlog,jsonlog", raw: "csvlog,jsonlog", category: "Reporting and Logging / Where to Log", description: "Sets the destination for server log output.", extra: "Valid values are combinations of \"stderr\", \"syslog\", \"csvlog\", \"jsonlog\", and \"eventlog\", depending on the platform."},
	{name: "log_directory", kind: "string", context: "sighup", initial: "log", boot: "log", alternate: "a//b/../c/", show: "a/c", raw: "a/c", category: "Reporting and Logging / Where to Log", description: "Sets the destination directory for log files.", extra: "Can be specified as relative to the data directory or as absolute path."},
	{name: "log_disconnections", kind: "bool", context: "superuser-backend", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs end of a session, including duration."},
	{name: "log_duration", kind: "bool", context: "superuser", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs the duration of each completed SQL statement."},
	{name: "log_error_verbosity", kind: "enum", context: "superuser", initial: "default", boot: "default", alternate: "VERBOSE", show: "verbose", raw: "verbose", category: "Reporting and Logging / What to Log", description: "Sets the verbosity of logged messages.", enums: "{terse,default,verbose}"},
	{name: "log_file_mode", kind: "integer", context: "sighup", initial: "0600", boot: "384", alternate: "0640", show: "0640", raw: "416", category: "Reporting and Logging / Where to Log", description: "Sets the file permissions for log files.", extra: "The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)", min: "0", max: "511"},
	{name: "log_filename", kind: "string", context: "sighup", initial: "postgresql-%Y-%m-%d_%H%M%S.log", boot: "postgresql-%Y-%m-%d_%H%M%S.log", alternate: "probe-%Y.log", show: "probe-%Y.log", raw: "probe-%Y.log", category: "Reporting and Logging / Where to Log", description: "Sets the file name pattern for log files."},
	{name: "log_hostname", kind: "bool", context: "sighup", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs the host name in the connection logs.", extra: "By default, connection logs only show the IP address of the connecting host. If you want them to show the host name you can turn this on, but depending on your host name resolution setup it might impose a non-negligible performance penalty."},
	{name: "log_line_prefix", kind: "string", context: "sighup", initial: "%m [%p] ", boot: "%m [%p] ", alternate: "%q:%m ", show: "%q:%m ", raw: "%q:%m ", category: "Reporting and Logging / What to Log", description: "Controls information prefixed to each log line.", extra: "An empty string means no prefix."},
	{name: "log_lock_failures", kind: "bool", context: "superuser", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs lock failures."},
	{name: "log_lock_waits", kind: "bool", context: "superuser", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs long lock waits."},
	{name: "log_min_duration_sample", kind: "integer", context: "superuser", initial: "-1", boot: "-1", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Sets the minimum execution time above which a sample of statements will be logged. Sampling is determined by \"log_statement_sample_rate\".", extra: "-1 disables sampling. 0 means sample all statements.", min: "-1", max: "2147483647", unit: "ms"},
	{name: "log_min_duration_statement", kind: "integer", context: "superuser", initial: "-1", boot: "-1", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Sets the minimum execution time above which all statements will be logged.", extra: "-1 disables logging statement durations. 0 means log all statement durations.", min: "-1", max: "2147483647", unit: "ms"},
	{name: "log_min_error_statement", kind: "enum", context: "superuser", initial: "error", boot: "error", alternate: "INFO", show: "info", raw: "info", category: "Reporting and Logging / When to Log", description: "Causes all statements generating error at or above this level to be logged.", extra: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", enums: "{debug5,debug4,debug3,debug2,debug1,info,notice,warning,error,log,fatal,panic}"},
	{name: "log_min_messages", kind: "enum", context: "superuser", initial: "warning", boot: "warning", alternate: "PANIC", show: "panic", raw: "panic", category: "Reporting and Logging / When to Log", description: "Sets the message levels that are logged.", extra: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", enums: "{debug5,debug4,debug3,debug2,debug1,info,notice,warning,error,log,fatal,panic}"},
	{name: "log_parameter_max_length", kind: "integer", context: "superuser", initial: "-1", boot: "-1", alternate: "1kB", show: "1kB", raw: "1024", category: "Reporting and Logging / What to Log", description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements.", extra: "-1 means log values in full.", min: "-1", max: "1073741823", unit: "B"},
	{name: "log_parameter_max_length_on_error", kind: "integer", context: "user", initial: "0", boot: "0", alternate: "1kB", show: "1kB", raw: "1024", category: "Reporting and Logging / What to Log", description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements, on error.", extra: "-1 means log values in full.", min: "-1", max: "1073741823", unit: "B"},
	{name: "log_recovery_conflict_waits", kind: "bool", context: "sighup", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs standby recovery conflict waits."},
	{name: "log_replication_commands", kind: "bool", context: "superuser", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs each replication command."},
	{name: "log_rotation_age", kind: "integer", context: "sighup", initial: "1d", boot: "1440", alternate: "2h", show: "2h", raw: "120", category: "Reporting and Logging / Where to Log", description: "Sets the amount of time to wait before forcing log file rotation.", extra: "0 disables time-based creation of new log files.", min: "0", max: "35791394", unit: "min"},
	{name: "log_rotation_size", kind: "integer", context: "sighup", initial: "10MB", boot: "10240", alternate: "2MB", show: "2MB", raw: "2048", category: "Reporting and Logging / Where to Log", description: "Sets the maximum size a log file can reach before being rotated.", extra: "0 disables size-based creation of new log files.", min: "0", max: "2147483647", unit: "kB"},
	{name: "log_startup_progress_interval", kind: "integer", context: "sighup", initial: "10s", boot: "10000", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Time between progress updates for long-running startup operations.", extra: "0 disables progress updates.", min: "0", max: "2147483647", unit: "ms"},
	{name: "log_statement", kind: "enum", context: "superuser", initial: "none", boot: "none", alternate: "ALL", show: "all", raw: "all", category: "Reporting and Logging / What to Log", description: "Sets the type of statements logged.", enums: "{none,ddl,mod,all}"},
	{name: "log_statement_sample_rate", kind: "real", context: "superuser", initial: "1", boot: "1", alternate: "0.25", show: "0.25", raw: "0.25", category: "Reporting and Logging / When to Log", description: "Fraction of statements exceeding \"log_min_duration_sample\" to be logged.", extra: "Use a value between 0.0 (never log) and 1.0 (always log).", min: "0", max: "1"},
	{name: "log_temp_files", kind: "integer", context: "superuser", initial: "-1", boot: "-1", alternate: "2MB", show: "2MB", raw: "2048", category: "Reporting and Logging / What to Log", description: "Log the use of temporary files larger than this number of kilobytes.", extra: "-1 disables logging temporary files. 0 means log all temporary files.", min: "-1", max: "2147483647", unit: "kB"},
	{name: "log_timezone", kind: "string", context: "sighup", initial: "GMT", boot: "GMT", alternate: "Europe/Paris", show: "Europe/Paris", raw: "Europe/Paris", category: "Reporting and Logging / What to Log", description: "Sets the time zone to use in log messages."},
	{name: "log_transaction_sample_rate", kind: "real", context: "superuser", initial: "0", boot: "0", alternate: "0.5", show: "0.5", raw: "0.5", category: "Reporting and Logging / When to Log", description: "Sets the fraction of transactions from which to log all statements.", extra: "Use a value between 0.0 (never log) and 1.0 (log all statements for all transactions).", min: "0", max: "1"},
	{name: "log_truncate_on_rotation", kind: "bool", context: "sighup", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / Where to Log", description: "Truncate existing log files of same name during log rotation."},
	{name: "logging_collector", kind: "bool", context: "postmaster", initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / Where to Log", description: "Start a subprocess to capture stderr, csvlog and/or jsonlog into log files."},
	{name: "syslog_facility", kind: "enum", context: "sighup", initial: "local0", boot: "local0", alternate: "LOCAL7", show: "local7", raw: "local7", category: "Reporting and Logging / Where to Log", description: "Sets the syslog \"facility\" to be used when syslog enabled.", enums: "{local0,local1,local2,local3,local4,local5,local6,local7}"},
	{name: "syslog_ident", kind: "string", context: "sighup", initial: "postgres", boot: "postgres", alternate: "probe ident", show: "probe ident", raw: "probe ident", category: "Reporting and Logging / Where to Log", description: "Sets the program name used to identify PostgreSQL messages in syslog."},
	{name: "syslog_sequence_numbers", kind: "bool", context: "sighup", initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Where to Log", description: "Add sequence number to syslog messages to avoid duplicate suppression."},
	{name: "syslog_split_messages", kind: "bool", context: "sighup", initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Where to Log", description: "Split messages sent to syslog by lines and to fit into 1024 bytes."},
	{name: "update_process_title", kind: "bool", context: "superuser", initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Process Title", description: "Updates the process title to show the active SQL command.", extra: "Enables updating of the process title every time a new SQL command is received by the server."},
}

func loggingMutable(tc loggingSetting) bool {
	return tc.context == "user" || tc.context == "superuser"
}

func loggingRestricted(tc loggingSetting) bool {
	return tc.name == "log_directory" || tc.name == "log_filename"
}

func loggingContext(tc loggingSetting) fixture.Diagnostic {
	return developerContext(developerSetting{name: tc.name, context: tc.context})
}

func loggingInvalid(tc loggingSetting, input string) fixture.Diagnostic {
	d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "%s": "%s"`, tc.name, input))
	if tc.kind == "bool" {
		d.Message = fmt.Sprintf("parameter %q requires a Boolean value", tc.name)
	}
	if tc.kind == "enum" {
		d.Hint = "Available values: " + strings.ReplaceAll(strings.Trim(tc.enums.(string), "{}"), ",", ", ") + "."
	}
	return d
}

// Fixture initialization differs deliberately from native builtin defaults.
func (s *physicalCase) loggingInitial(tc loggingSetting) (show, raw, source string) {
	show, raw, source = tc.initial, tc.boot, "default"
	if tc.name == "log_file_mode" {
		raw = "0600"
	}
	if s.pg {
		switch tc.name {
		case "log_timezone":
			show, raw, source = "Etc/UTC", "Etc/UTC", "configuration file"
		case "log_line_prefix":
			show, raw, source = "timeout-oracle|%p|%c|%e|", "timeout-oracle|%p|%c|%e|", "command line"
		}
	}
	return
}

func (s *physicalCase) loggingRead(tc loggingSetting, value string, prepared bool) {
	s.developerRead(developerSetting{name: tc.name}, value, prepared)
}

func (s *physicalCase) loggingMetadata(tc loggingSetting, value, source string, prepared bool) {
	var extra any
	if tc.extra != "" {
		extra = tc.extra
	}
	_, reset, _ := s.loggingInitial(tc)
	if tc.name == "log_file_mode" {
		reset = "384"
	}
	want := []any{tc.name, value, tc.unit, tc.category, tc.description, extra, tc.context, tc.kind, source, tc.min, tc.max, tc.enums, tc.boot, reset, nil, nil, false}
	// Configuration provenance is environment-owned. Validate its shape first,
	// then compare every other cell against independent literal expectations.
	query := "SELECT " + physicalColumnsSQL + " FROM pg_settings WHERE name='" + tc.name + "'"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query)
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	if !rows.Next() {
		s.t.Fatal("missing logging metadata", tc.name, rows.Err())
	}
	cells, dest := make([]any, len(want)), make([]any, len(want))
	for i := range cells {
		dest[i] = &cells[i]
	}
	if err := rows.Scan(dest...); err != nil {
		s.t.Fatal(err)
	}
	for i := range cells {
		cells[i] = fixture.Normalize(cells[i])
	}
	if s.pg && tc.name == "log_timezone" && cells[14] != nil {
		file, ok := cells[14].(string)
		line, lineOK := cells[15].(int64)
		if !ok || !path.IsAbs(file) || !strings.HasSuffix(file, "/postgresql.conf") || !lineOK || line <= 0 {
			s.t.Fatal("invalid logging configuration provenance", cells)
		}
		cells[14], cells[15] = nil, nil
	}
	if !reflect.DeepEqual(cells, want) {
		s.t.Fatalf("%s metadata got %#v want %#v", tc.name, cells, want)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatal("logging metadata cardinality", rows.Err())
	}
}

func (s *physicalCase) loggingConfig(tc loggingSetting, input any, want string, local, prepared bool) {
	step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{want}}, tc.name, input, local)
	step.Prepared = prepared
	s.run(step)
}

func (s *physicalCase) loggingAll(allowed, prepared bool) {
	want := map[string][]string{}
	var names []string
	var projection [][]any
	for _, tc := range loggingSettings {
		names = append(names, physicalLiteral(tc.name))
		if !allowed && loggingRestricted(tc) {
			continue
		}
		initial, raw, _ := s.loggingInitial(tc)
		want[tc.name] = []string{initial, tc.description}
		projection = append(projection, []any{tc.name, raw, tc.description})
	}
	step := fixture.Select("SELECT name,setting,short_desc FROM pg_settings WHERE name IN ("+strings.Join(names, ",")+") ORDER BY name", []string{"name", "setting", "short_desc"}, []string{"TEXT", "TEXT", "TEXT"}, projection)
	step.Prepared = prepared
	s.run(step)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL")
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL")
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) {
		s.t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		s.t.Fatal(types, err)
	}
	for _, column := range types {
		nullable, known := column.Nullable()
		if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() || known == s.pg || nullable {
			s.t.Fatal("SHOW ALL metadata", column)
		}
	}
	found, previous := 0, ""
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			s.t.Fatal(err)
		}
		if strings.ToLower(name) <= previous {
			s.t.Fatal("SHOW ALL order", previous, name)
		}
		previous = strings.ToLower(name)
		for _, tc := range loggingSettings {
			if name != tc.name {
				continue
			}
			expected, ok := want[name]
			if !ok || !reflect.DeepEqual(expected, []string{value, description}) {
				s.t.Fatal("SHOW ALL logging row", name, value, description)
			}
			found++
		}
	}
	if rows.Err() != nil || found != len(want) {
		s.t.Fatal("SHOW ALL logging cardinality", found, len(want), rows.Err())
	}
}

func loggingNames() string {
	names := make([]string, len(loggingSettings))
	for i, tc := range loggingSettings {
		names[i] = physicalLiteral(tc.name)
	}
	return strings.Join(names, ",")
}
