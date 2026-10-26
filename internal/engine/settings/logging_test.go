package settings

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

type loggingExpectation struct {
	name                                                              string
	kind                                                              Type
	access                                                            Access
	unit                                                              Unit
	initial, boot, alternate, show, raw, category, description, extra string
	min, max                                                          float64
	enums                                                             []string
}

// Independent release expectations, not copied from the native registry.
var loggingExpectations = []loggingExpectation{
	{name: "application_name", kind: String, access: Session, unit: UnitNone, initial: "", boot: "", alternate: "logging_probe", show: "logging_probe", raw: "logging_probe", category: "Reporting and Logging / What to Log", description: "Sets the application name to be reported in statistics and logs.", extra: "", min: 0, max: 0},
	{name: "cluster_name", kind: String, access: StartupOnly, unit: UnitNone, initial: "", boot: "", alternate: "probe", show: "probe", raw: "probe", category: "Reporting and Logging / Process Title", description: "Sets the name of the cluster, which is included in the process title.", extra: "", min: 0, max: 0},
	{name: "debug_pretty_print", kind: Boolean, access: Session, unit: UnitNone, initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / What to Log", description: "Indents parse and plan tree displays.", extra: "", min: 0, max: 0},
	{name: "debug_print_parse", kind: Boolean, access: Session, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs each query's parse tree.", extra: "", min: 0, max: 0},
	{name: "event_source", kind: String, access: StartupOnly, unit: UnitNone, initial: "PostgreSQL", boot: "PostgreSQL", alternate: "probe", show: "probe", raw: "probe", category: "Reporting and Logging / Where to Log", description: "Sets the application name used to identify PostgreSQL messages in the event log.", extra: "", min: 0, max: 0},
	{name: "log_autovacuum_min_duration", kind: Integer, access: Sighup, unit: Milliseconds, initial: "10min", boot: "600000", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / What to Log", description: "Sets the minimum execution time above which autovacuum actions will be logged.", extra: "-1 disables logging autovacuum actions. 0 means log all autovacuum actions.", min: -1, max: 2147483647},
	{name: "log_checkpoints", kind: Boolean, access: Sighup, unit: UnitNone, initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / What to Log", description: "Logs each checkpoint.", extra: "", min: 0, max: 0},
	{name: "log_connections", kind: String, access: SuperuserBackend, unit: UnitNone, initial: "", boot: "", alternate: "receipt, authentication", show: "receipt, authentication", raw: "receipt, authentication", category: "Reporting and Logging / What to Log", description: "Logs specified aspects of connection establishment and setup.", extra: "", min: 0, max: 0},
	{name: "log_destination", kind: String, access: Sighup, unit: UnitNone, initial: "stderr", boot: "stderr", alternate: "csvlog,jsonlog", show: "csvlog,jsonlog", raw: "csvlog,jsonlog", category: "Reporting and Logging / Where to Log", description: "Sets the destination for server log output.", extra: "Valid values are combinations of \"stderr\", \"syslog\", \"csvlog\", \"jsonlog\", and \"eventlog\", depending on the platform.", min: 0, max: 0},
	{name: "log_directory", kind: String, access: Sighup, unit: UnitNone, initial: "log", boot: "log", alternate: "a//b/../c/", show: "a/c", raw: "a/c", category: "Reporting and Logging / Where to Log", description: "Sets the destination directory for log files.", extra: "Can be specified as relative to the data directory or as absolute path.", min: 0, max: 0},
	{name: "log_disconnections", kind: Boolean, access: SuperuserBackend, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs end of a session, including duration.", extra: "", min: 0, max: 0},
	{name: "log_duration", kind: Boolean, access: Privileged, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs the duration of each completed SQL statement.", extra: "", min: 0, max: 0},
	{name: "log_error_verbosity", kind: Enum, access: Privileged, unit: UnitNone, initial: "default", boot: "default", alternate: "VERBOSE", show: "verbose", raw: "verbose", category: "Reporting and Logging / What to Log", description: "Sets the verbosity of logged messages.", extra: "", min: 0, max: 0, enums: []string{"terse", "default", "verbose"}},
	{name: "log_file_mode", kind: Integer, access: Sighup, unit: UnitNone, initial: "0600", boot: "384", alternate: "0640", show: "0640", raw: "416", category: "Reporting and Logging / Where to Log", description: "Sets the file permissions for log files.", extra: "The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)", min: 0, max: 511},
	{name: "log_filename", kind: String, access: Sighup, unit: UnitNone, initial: "postgresql-%Y-%m-%d_%H%M%S.log", boot: "postgresql-%Y-%m-%d_%H%M%S.log", alternate: "probe-%Y.log", show: "probe-%Y.log", raw: "probe-%Y.log", category: "Reporting and Logging / Where to Log", description: "Sets the file name pattern for log files.", extra: "", min: 0, max: 0},
	{name: "log_hostname", kind: Boolean, access: Sighup, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs the host name in the connection logs.", extra: "By default, connection logs only show the IP address of the connecting host. If you want them to show the host name you can turn this on, but depending on your host name resolution setup it might impose a non-negligible performance penalty.", min: 0, max: 0},
	{name: "log_line_prefix", kind: String, access: Sighup, unit: UnitNone, initial: "%m [%p] ", boot: "%m [%p] ", alternate: "%q:%m ", show: "%q:%m ", raw: "%q:%m ", category: "Reporting and Logging / What to Log", description: "Controls information prefixed to each log line.", extra: "An empty string means no prefix.", min: 0, max: 0},
	{name: "log_lock_failures", kind: Boolean, access: Privileged, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs lock failures.", extra: "", min: 0, max: 0},
	{name: "log_lock_waits", kind: Boolean, access: Privileged, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs long lock waits.", extra: "", min: 0, max: 0},
	{name: "log_min_duration_sample", kind: Integer, access: Privileged, unit: Milliseconds, initial: "-1", boot: "-1", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Sets the minimum execution time above which a sample of statements will be logged. Sampling is determined by \"log_statement_sample_rate\".", extra: "-1 disables sampling. 0 means sample all statements.", min: -1, max: 2147483647},
	{name: "log_min_duration_statement", kind: Integer, access: Privileged, unit: Milliseconds, initial: "-1", boot: "-1", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Sets the minimum execution time above which all statements will be logged.", extra: "-1 disables logging statement durations. 0 means log all statement durations.", min: -1, max: 2147483647},
	{name: "log_min_error_statement", kind: Enum, access: Privileged, unit: UnitNone, initial: "error", boot: "error", alternate: "INFO", show: "info", raw: "info", category: "Reporting and Logging / When to Log", description: "Causes all statements generating error at or above this level to be logged.", extra: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", min: 0, max: 0, enums: []string{"debug5", "debug4", "debug3", "debug2", "debug1", "info", "notice", "warning", "error", "log", "fatal", "panic"}},
	{name: "log_min_messages", kind: Enum, access: Privileged, unit: UnitNone, initial: "warning", boot: "warning", alternate: "PANIC", show: "panic", raw: "panic", category: "Reporting and Logging / When to Log", description: "Sets the message levels that are logged.", extra: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", min: 0, max: 0, enums: []string{"debug5", "debug4", "debug3", "debug2", "debug1", "info", "notice", "warning", "error", "log", "fatal", "panic"}},
	{name: "log_parameter_max_length", kind: Integer, access: Privileged, unit: Bytes, initial: "-1", boot: "-1", alternate: "1kB", show: "1kB", raw: "1024", category: "Reporting and Logging / What to Log", description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements.", extra: "-1 means log values in full.", min: -1, max: 1073741823},
	{name: "log_parameter_max_length_on_error", kind: Integer, access: Session, unit: Bytes, initial: "0", boot: "0", alternate: "1kB", show: "1kB", raw: "1024", category: "Reporting and Logging / What to Log", description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements, on error.", extra: "-1 means log values in full.", min: -1, max: 1073741823},
	{name: "log_recovery_conflict_waits", kind: Boolean, access: Sighup, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs standby recovery conflict waits.", extra: "", min: 0, max: 0},
	{name: "log_replication_commands", kind: Boolean, access: Privileged, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / What to Log", description: "Logs each replication command.", extra: "", min: 0, max: 0},
	{name: "log_rotation_age", kind: Integer, access: Sighup, unit: Minutes, initial: "1d", boot: "1440", alternate: "2h", show: "2h", raw: "120", category: "Reporting and Logging / Where to Log", description: "Sets the amount of time to wait before forcing log file rotation.", extra: "0 disables time-based creation of new log files.", min: 0, max: 35791394},
	{name: "log_rotation_size", kind: Integer, access: Sighup, unit: Kilobytes, initial: "10MB", boot: "10240", alternate: "2MB", show: "2MB", raw: "2048", category: "Reporting and Logging / Where to Log", description: "Sets the maximum size a log file can reach before being rotated.", extra: "0 disables size-based creation of new log files.", min: 0, max: 2147483647},
	{name: "log_startup_progress_interval", kind: Integer, access: Sighup, unit: Milliseconds, initial: "10s", boot: "10000", alternate: "2s", show: "2s", raw: "2000", category: "Reporting and Logging / When to Log", description: "Time between progress updates for long-running startup operations.", extra: "0 disables progress updates.", min: 0, max: 2147483647},
	{name: "log_statement", kind: Enum, access: Privileged, unit: UnitNone, initial: "none", boot: "none", alternate: "ALL", show: "all", raw: "all", category: "Reporting and Logging / What to Log", description: "Sets the type of statements logged.", extra: "", min: 0, max: 0, enums: []string{"none", "ddl", "mod", "all"}},
	{name: "log_statement_sample_rate", kind: Floating, access: Privileged, unit: UnitNone, initial: "1", boot: "1", alternate: "0.25", show: "0.25", raw: "0.25", category: "Reporting and Logging / When to Log", description: "Fraction of statements exceeding \"log_min_duration_sample\" to be logged.", extra: "Use a value between 0.0 (never log) and 1.0 (always log).", min: 0, max: 1},
	{name: "log_temp_files", kind: Integer, access: Privileged, unit: Kilobytes, initial: "-1", boot: "-1", alternate: "2MB", show: "2MB", raw: "2048", category: "Reporting and Logging / What to Log", description: "Log the use of temporary files larger than this number of kilobytes.", extra: "-1 disables logging temporary files. 0 means log all temporary files.", min: -1, max: 2147483647},
	{name: "log_timezone", kind: String, access: Sighup, unit: UnitNone, initial: "GMT", boot: "GMT", alternate: "Europe/Paris", show: "Europe/Paris", raw: "Europe/Paris", category: "Reporting and Logging / What to Log", description: "Sets the time zone to use in log messages.", extra: "", min: 0, max: 0},
	{name: "log_transaction_sample_rate", kind: Floating, access: Privileged, unit: UnitNone, initial: "0", boot: "0", alternate: "0.5", show: "0.5", raw: "0.5", category: "Reporting and Logging / When to Log", description: "Sets the fraction of transactions from which to log all statements.", extra: "Use a value between 0.0 (never log) and 1.0 (log all statements for all transactions).", min: 0, max: 1},
	{name: "log_truncate_on_rotation", kind: Boolean, access: Sighup, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / Where to Log", description: "Truncate existing log files of same name during log rotation.", extra: "", min: 0, max: 0},
	{name: "logging_collector", kind: Boolean, access: StartupOnly, unit: UnitNone, initial: "off", boot: "off", alternate: "on", show: "on", raw: "on", category: "Reporting and Logging / Where to Log", description: "Start a subprocess to capture stderr, csvlog and/or jsonlog into log files.", extra: "", min: 0, max: 0},
	{name: "syslog_facility", kind: Enum, access: Sighup, unit: UnitNone, initial: "local0", boot: "local0", alternate: "LOCAL7", show: "local7", raw: "local7", category: "Reporting and Logging / Where to Log", description: "Sets the syslog \"facility\" to be used when syslog enabled.", extra: "", min: 0, max: 0, enums: []string{"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"}},
	{name: "syslog_ident", kind: String, access: Sighup, unit: UnitNone, initial: "postgres", boot: "postgres", alternate: "probe ident", show: "probe ident", raw: "probe ident", category: "Reporting and Logging / Where to Log", description: "Sets the program name used to identify PostgreSQL messages in syslog.", extra: "", min: 0, max: 0},
	{name: "syslog_sequence_numbers", kind: Boolean, access: Sighup, unit: UnitNone, initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Where to Log", description: "Add sequence number to syslog messages to avoid duplicate suppression.", extra: "", min: 0, max: 0},
	{name: "syslog_split_messages", kind: Boolean, access: Sighup, unit: UnitNone, initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Where to Log", description: "Split messages sent to syslog by lines and to fit into 1024 bytes.", extra: "", min: 0, max: 0},
	{name: "update_process_title", kind: Boolean, access: Privileged, unit: UnitNone, initial: "on", boot: "on", alternate: "off", show: "off", raw: "off", category: "Reporting and Logging / Process Title", description: "Updates the process title to show the active SQL command.", extra: "Enables updating of the process title every time a new SQL command is received by the server.", min: 0, max: 0},
}

func TestLoggingSettingsDefinitions(t *testing.T) {
	r := NewRegistry(nil, false)
	seen := map[string]bool{}
	for _, tc := range loggingExpectations {
		if seen[tc.name] {
			t.Fatal("duplicate", tc.name)
		}
		seen[tc.name] = true
		d, err := r.Lookup(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if d.Type != tc.kind || d.Access != tc.access || d.Unit != tc.unit || d.BootDefault != tc.initial || d.Format(d.DefaultValue()) != tc.initial || d.Raw(d.BootValue()) != tc.boot || d.Category != tc.category || d.Description != tc.description || d.ExtraDescription != tc.extra || d.BootDefaultNull || d.NativeStartup != nil || d.ReadRestricted != (tc.name == "log_directory" || tc.name == "log_filename") || !reflect.DeepEqual(d.EnumValues, tc.enums) {
			t.Fatalf("%s definition: %+v", tc.name, d)
		}
		if tc.kind == Integer && (float64(d.IntegerMin) != tc.min || float64(d.IntegerMax) != tc.max) || tc.kind == Floating && (d.FloatMin != tc.min || d.FloatMax != tc.max) {
			t.Fatal("bounds", tc.name, d)
		}
	}
	if len(seen) != 42 || len(loggingBuiltins) != 41 {
		t.Fatal("family inventory", len(seen), len(loggingBuiltins))
	}
	count := 0
	for _, name := range r.Order() {
		if name == "application_name" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("application_name count", count)
	}
}

func TestLoggingSettingsFormatting(t *testing.T) {
	r := NewRegistry(nil, false)
	for _, tc := range loggingExpectations {
		d, _ := r.Lookup(tc.name)
		v, err := d.Parse(tc.alternate)
		if err != nil || d.Format(v) != tc.show || d.Raw(v) != tc.raw {
			t.Fatalf("%s alternate got %q/%q %v", tc.name, d.Format(v), d.Raw(v), err)
		}
		current := tc.raw
		if tc.name == "log_file_mode" {
			current = "0640"
		}
		if d.CurrentRaw(v) != current {
			t.Fatal("current raw", tc.name, d.CurrentRaw(v), current)
		}
	}
	for _, tc := range []struct{ input, show, raw string }{{"384", "0600", "384"}, {"0", "0000", "0"}, {"0777", "0777", "511"}, {"0x1ff", "0777", "511"}} {
		d, _ := r.Lookup("log_file_mode")
		v, err := d.Parse(tc.input)
		if err != nil || d.Format(v) != tc.show || d.CurrentRaw(v) != tc.show || d.Raw(v) != tc.raw {
			t.Fatal(tc, v, err)
		}
	}
}

func TestLoggingSettingsValidation(t *testing.T) {
	r := NewRegistry(nil, false)
	valid := []struct{ name, input, want string }{
		{"log_connections", "", ""}, {"log_connections", " \t", " \t"}, {"log_connections", `"ReCeIpT", ALL,receipt`, `"ReCeIpT", ALL,receipt`},
		{"log_destination", "", ""}, {"log_destination", `"STDERR", csvlog,jsonlog,syslog,stderr`, `"STDERR", csvlog,jsonlog,syslog,stderr`},
		{"log_directory", "a//b/../c/", "a/c"}, {"log_directory", "/../../a/", "/a"}, {"log_directory", "a/..", "."}, {"log_directory", "", ""}, {"log_directory", `a\b`, `a\b`},
		{"cluster_name", "é", `\xc3\xa9`}, {"cluster_name", "x\ny", `x\x0ay`}, {"cluster_name", strings.Repeat("x", 62) + "é", strings.Repeat("x", 62)},
		{"log_timezone", "GMT", "GMT"}, {"log_timezone", "utc", "UTC"}, {"log_timezone", "UTC", "UTC"}, {"log_timezone", "Europe/Paris", "Europe/Paris"}, {"log_timezone", "Europe/Amsterdam", "Europe/Amsterdam"}, {"log_timezone", "UTC0:01", "UTC0:01"},
		{"log_min_messages", "debug", "debug2"}, {"log_min_error_statement", "INFO", "info"},
		{"log_timezone", "2", "2"},
		{"log_timezone", "INTERVAL '2 hours'", "INTERVAL '2 HOURS'"},
		{"event_source", "", ""}, {"syslog_ident", "probe ident", "probe ident"}, {"log_line_prefix", "%q:%m ", "%q:%m "}, {"log_filename", "probe-%Y.log", "probe-%Y.log"},
	}
	for _, alias := range []string{"on", "true", "yes", "1", "off", "false", "no", "0"} {
		valid = append(valid, struct{ name, input, want string }{"log_connections", alias, alias})
	}
	for _, tc := range valid {
		d, _ := r.Lookup(tc.name)
		v, err := d.Parse(tc.input)
		if err != nil || d.Format(v) != tc.want {
			t.Fatalf("%s %q got %q %v", tc.name, tc.input, d.Format(v), err)
		}
	}
	invalid := []struct{ name, input, detail string }{
		{"log_connections", "on, receipt", `Cannot specify log_connections option "on" in a list with other options.`},
		{"log_connections", "receipt, on", `Invalid option "on".`}, {"log_connections", "receipt,", `Invalid list syntax in parameter "log_connections".`},
		{"log_connections", `"rece""ipt"`, `Invalid option "rece"ipt".`}, {"log_connections", `" receipt "`, `Invalid option " receipt ".`},
		{"log_destination", "eventlog", `Unrecognized key word: "eventlog".`}, {"log_destination", "stderr,", "List syntax is invalid."}, {"log_destination", "stderr syslog", "List syntax is invalid."},
		{"log_timezone", "right/UTC", ""}, {"log_timezone", "unknown/zone", ""}, {"log_timezone", "INTERVAL", ""},
	}
	for _, input := range []string{",receipt", "receipt,,authentication", `"receipt`, "receipt authentication"} {
		invalid = append(invalid, struct{ name, input, detail string }{"log_connections", input, `Invalid list syntax in parameter "log_connections".`})
	}
	for _, input := range []string{strings.Repeat("A", 64), strings.Repeat("a", 62) + "é"} {
		clipped := strings.Repeat("a", 63)
		if strings.Contains(input, "é") {
			clipped = strings.Repeat("a", 62)
		}
		invalid = append(invalid, struct{ name, input, detail string }{"log_connections", input, fmt.Sprintf(`Invalid option "%s".`, clipped)})
	}
	for _, v := range []string{"t", "tr", "y", "f", "n", "of"} {
		invalid = append(invalid, struct{ name, input, detail string }{"log_connections", v, fmt.Sprintf(`Invalid option "%s".`, v)})
	}
	for _, tc := range invalid {
		d, _ := r.Lookup(tc.name)
		_, err := d.Parse(tc.input)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "22023" || e.Message != fmt.Sprintf(`invalid value for parameter "%s": "%s"`, tc.name, tc.input) || e.Detail != tc.detail || e.Hint != "" {
			t.Fatalf("%s %q: %+v", tc.name, tc.input, err)
		}
	}
	for _, input := range []string{"UTC0:00:30", "UTC-0:00:30"} {
		d, _ := r.Lookup("log_timezone")
		_, err := d.Parse(input)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "22023" || e.Message != fmt.Sprintf(`time zone "%s" appears to use leap seconds`, input) || e.Detail != "PostgreSQL does not support leap seconds." {
			t.Fatal(input, err)
		}
	}
	for _, tc := range loggingExpectations {
		d, _ := r.Lookup(tc.name)
		switch tc.kind {
		case Boolean:
			for _, input := range []string{"o", "broken", " on"} {
				_, err := d.Parse(input)
				var e *sqlstate.Error
				if !errors.As(err, &e) || e.Message != fmt.Sprintf(`parameter "%s" requires a Boolean value`, tc.name) {
					t.Fatal(tc.name, input, err)
				}
			}
		case Enum:
			for _, input := range []string{"broken", " " + tc.initial, tc.initial[:1]} {
				_, err := d.Parse(input)
				var e *sqlstate.Error
				if !errors.As(err, &e) || e.Hint != "Available values: "+strings.Join(tc.enums, ", ")+"." {
					t.Fatal(tc.name, input, err)
				}
			}
		case Integer, Floating:
			for _, input := range []string{fmt.Sprint(tc.min), fmt.Sprint(tc.max)} {
				if _, err := d.Parse(input); err != nil {
					t.Fatal("inclusive bound", tc.name, input, err)
				}
			}
			for _, input := range []string{fmt.Sprint(tc.min - 1), fmt.Sprint(tc.max + 1), "NaN", "broken"} {
				if _, err := d.Parse(input); err == nil {
					t.Fatal("invalid number", tc.name, input)
				}
			}
		}
	}
}
