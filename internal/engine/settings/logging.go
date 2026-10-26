package settings

import (
	"fmt"
	"path"
	"strings"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

const (
	loggingWhere = "Reporting and Logging / Where to Log"
	loggingWhen  = "Reporting and Logging / When to Log"
	loggingWhat  = "Reporting and Logging / What to Log"
	loggingTitle = "Reporting and Logging / Process Title"
)

// Linux-style release descriptors. These store configuration only: there is no
// collector, file, syslog, process-title, sampling or background logging consumer.
// application_name remains owned by builtins and its existing notice consumer.
var loggingBuiltins = []Definition{
	{Name: "cluster_name", Type: String, Access: StartupOnly, Category: loggingTitle, Description: "Sets the name of the cluster, which is included in the process title.", Normalize: normalizeApplicationName},
	{Name: "debug_pretty_print", Type: Boolean, BootDefault: "on", Category: loggingWhat, Description: "Indents parse and plan tree displays."},
	{Name: "debug_print_parse", Type: Boolean, BootDefault: "off", Category: loggingWhat, Description: "Logs each query's parse tree."},
	{Name: "event_source", Type: String, Access: StartupOnly, BootDefault: "PostgreSQL", Category: loggingWhere, Description: "Sets the application name used to identify PostgreSQL messages in the event log."},
	{Name: "log_autovacuum_min_duration", Type: Integer, Access: Sighup, BootDefault: "600000", Unit: Milliseconds, IntegerMin: -1, IntegerMax: 2147483647, Category: loggingWhat, Description: "Sets the minimum execution time above which autovacuum actions will be logged.", ExtraDescription: "-1 disables logging autovacuum actions. 0 means log all autovacuum actions."},
	{Name: "log_checkpoints", Type: Boolean, Access: Sighup, BootDefault: "on", Category: loggingWhat, Description: "Logs each checkpoint."},
	{Name: "log_connections", Type: String, Access: SuperuserBackend, Category: loggingWhat, Description: "Logs specified aspects of connection establishment and setup.", Normalize: normalizeLogConnections},
	{Name: "log_destination", Type: String, Access: Sighup, BootDefault: "stderr", Category: loggingWhere, Description: "Sets the destination for server log output.", ExtraDescription: "Valid values are combinations of \"stderr\", \"syslog\", \"csvlog\", \"jsonlog\", and \"eventlog\", depending on the platform.", Normalize: normalizeLogDestination},
	{Name: "log_directory", Type: String, Access: Sighup, BootDefault: "log", ReadRestricted: true, Category: loggingWhere, Description: "Sets the destination directory for log files.", ExtraDescription: "Can be specified as relative to the data directory or as absolute path.", Normalize: normalizeLogDirectory},
	{Name: "log_disconnections", Type: Boolean, Access: SuperuserBackend, BootDefault: "off", Category: loggingWhat, Description: "Logs end of a session, including duration."},
	{Name: "log_duration", Type: Boolean, Access: Privileged, BootDefault: "off", Category: loggingWhat, Description: "Logs the duration of each completed SQL statement."},
	{Name: "log_error_verbosity", Type: Enum, Access: Privileged, BootDefault: "default", EnumValues: []string{"terse", "default", "verbose"}, Category: loggingWhat, Description: "Sets the verbosity of logged messages."},
	{Name: "log_file_mode", Type: Integer, Access: Sighup, BootDefault: "0600", IntegerMax: 511, Category: loggingWhere, Description: "Sets the file permissions for log files.", ExtraDescription: "The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)"},
	{Name: "log_filename", Type: String, Access: Sighup, BootDefault: "postgresql-%Y-%m-%d_%H%M%S.log", ReadRestricted: true, Category: loggingWhere, Description: "Sets the file name pattern for log files."},
	{Name: "log_hostname", Type: Boolean, Access: Sighup, BootDefault: "off", Category: loggingWhat, Description: "Logs the host name in the connection logs.", ExtraDescription: "By default, connection logs only show the IP address of the connecting host. If you want them to show the host name you can turn this on, but depending on your host name resolution setup it might impose a non-negligible performance penalty."},
	{Name: "log_line_prefix", Type: String, Access: Sighup, BootDefault: "%m [%p] ", Category: loggingWhat, Description: "Controls information prefixed to each log line.", ExtraDescription: "An empty string means no prefix."},
	{Name: "log_lock_failures", Type: Boolean, Access: Privileged, BootDefault: "off", Category: loggingWhat, Description: "Logs lock failures."},
	{Name: "log_lock_waits", Type: Boolean, Access: Privileged, BootDefault: "off", Category: loggingWhat, Description: "Logs long lock waits."},
	{Name: "log_min_duration_sample", Type: Integer, Access: Privileged, BootDefault: "-1", Unit: Milliseconds, IntegerMin: -1, IntegerMax: 2147483647, Category: loggingWhen, Description: "Sets the minimum execution time above which a sample of statements will be logged. Sampling is determined by \"log_statement_sample_rate\".", ExtraDescription: "-1 disables sampling. 0 means sample all statements."},
	{Name: "log_min_duration_statement", Type: Integer, Access: Privileged, BootDefault: "-1", Unit: Milliseconds, IntegerMin: -1, IntegerMax: 2147483647, Category: loggingWhen, Description: "Sets the minimum execution time above which all statements will be logged.", ExtraDescription: "-1 disables logging statement durations. 0 means log all statement durations."},
	{Name: "log_min_error_statement", Type: Enum, Access: Privileged, BootDefault: "error", EnumValues: serverMessageLevels, Category: loggingWhen, Description: "Causes all statements generating error at or above this level to be logged.", ExtraDescription: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", Normalize: normalizeLogMinErrorStatement},
	{Name: "log_min_messages", Type: Enum, Access: Privileged, BootDefault: "warning", EnumValues: serverMessageLevels, Category: loggingWhen, Description: "Sets the message levels that are logged.", ExtraDescription: "Each level includes all the levels that follow it. The later the level, the fewer messages are sent.", Normalize: normalizeLogMinMessages},
	{Name: "log_parameter_max_length", Type: Integer, Access: Privileged, BootDefault: "-1", Unit: Bytes, IntegerMin: -1, IntegerMax: 1073741823, Category: loggingWhat, Description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements.", ExtraDescription: "-1 means log values in full."},
	{Name: "log_parameter_max_length_on_error", Type: Integer, BootDefault: "0", Unit: Bytes, IntegerMin: -1, IntegerMax: 1073741823, Category: loggingWhat, Description: "Sets the maximum length in bytes of data logged for bind parameter values when logging statements, on error.", ExtraDescription: "-1 means log values in full."},
	{Name: "log_recovery_conflict_waits", Type: Boolean, Access: Sighup, BootDefault: "off", Category: loggingWhat, Description: "Logs standby recovery conflict waits."},
	{Name: "log_replication_commands", Type: Boolean, Access: Privileged, BootDefault: "off", Category: loggingWhat, Description: "Logs each replication command."},
	{Name: "log_rotation_age", Type: Integer, Access: Sighup, BootDefault: "1440", Unit: Minutes, IntegerMax: 35791394, Category: loggingWhere, Description: "Sets the amount of time to wait before forcing log file rotation.", ExtraDescription: "0 disables time-based creation of new log files."},
	{Name: "log_rotation_size", Type: Integer, Access: Sighup, BootDefault: "10240", Unit: Kilobytes, IntegerMax: 2147483647, Category: loggingWhere, Description: "Sets the maximum size a log file can reach before being rotated.", ExtraDescription: "0 disables size-based creation of new log files."},
	{Name: "log_startup_progress_interval", Type: Integer, Access: Sighup, BootDefault: "10000", Unit: Milliseconds, IntegerMax: 2147483647, Category: loggingWhen, Description: "Time between progress updates for long-running startup operations.", ExtraDescription: "0 disables progress updates."},
	{Name: "log_statement", Type: Enum, Access: Privileged, BootDefault: "none", EnumValues: []string{"none", "ddl", "mod", "all"}, Category: loggingWhat, Description: "Sets the type of statements logged."},
	{Name: "log_statement_sample_rate", Type: Floating, Access: Privileged, BootDefault: "1", FloatMax: 1, Category: loggingWhen, Description: "Fraction of statements exceeding \"log_min_duration_sample\" to be logged.", ExtraDescription: "Use a value between 0.0 (never log) and 1.0 (always log)."},
	{Name: "log_temp_files", Type: Integer, Access: Privileged, BootDefault: "-1", Unit: Kilobytes, IntegerMin: -1, IntegerMax: 2147483647, Category: loggingWhat, Description: "Log the use of temporary files larger than this number of kilobytes.", ExtraDescription: "-1 disables logging temporary files. 0 means log all temporary files."},
	{Name: "log_timezone", Type: String, Access: Sighup, BootDefault: "GMT", Category: loggingWhat, Description: "Sets the time zone to use in log messages.", Normalize: normalizeLogTimezone},
	{Name: "log_transaction_sample_rate", Type: Floating, Access: Privileged, BootDefault: "0", FloatMax: 1, Category: loggingWhen, Description: "Sets the fraction of transactions from which to log all statements.", ExtraDescription: "Use a value between 0.0 (never log) and 1.0 (log all statements for all transactions)."},
	{Name: "log_truncate_on_rotation", Type: Boolean, Access: Sighup, BootDefault: "off", Category: loggingWhere, Description: "Truncate existing log files of same name during log rotation."},
	{Name: "logging_collector", Type: Boolean, Access: StartupOnly, BootDefault: "off", Category: loggingWhere, Description: "Start a subprocess to capture stderr, csvlog and/or jsonlog into log files."},
	{Name: "syslog_facility", Type: Enum, Access: Sighup, BootDefault: "local0", EnumValues: []string{"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"}, Category: loggingWhere, Description: "Sets the syslog \"facility\" to be used when syslog enabled."},
	{Name: "syslog_ident", Type: String, Access: Sighup, BootDefault: "postgres", Category: loggingWhere, Description: "Sets the program name used to identify PostgreSQL messages in syslog."},
	{Name: "syslog_sequence_numbers", Type: Boolean, Access: Sighup, BootDefault: "on", Category: loggingWhere, Description: "Add sequence number to syslog messages to avoid duplicate suppression."},
	{Name: "syslog_split_messages", Type: Boolean, Access: Sighup, BootDefault: "on", Category: loggingWhere, Description: "Split messages sent to syslog by lines and to fit into 1024 bytes."},
	{Name: "update_process_title", Type: Boolean, Access: Privileged, BootDefault: "on", Category: loggingTitle, Description: "Updates the process title to show the active SQL command.", ExtraDescription: "Enables updating of the process title every time a new SQL command is received by the server."},
}

var serverMessageLevels = []string{"debug5", "debug4", "debug3", "debug2", "debug1", "info", "notice", "warning", "error", "log", "fatal", "panic"}

func normalizeServerMessageLevel(name, input string) (string, *sqlstate.Error, error) {
	if developerKeyword(input, "debug") {
		return "debug2", nil, nil
	}
	for _, level := range serverMessageLevels {
		if developerKeyword(input, level) {
			return level, nil, nil
		}
	}
	return "", nil, parameterError(fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, input), "Available values: "+strings.Join(serverMessageLevels, ", ")+".")
}

func normalizeLogMinMessages(input string) (string, *sqlstate.Error, error) {
	return normalizeServerMessageLevel("log_min_messages", input)
}

func normalizeLogMinErrorStatement(input string) (string, *sqlstate.Error, error) {
	return normalizeServerMessageLevel("log_min_error_statement", input)
}

func normalizeLogDirectory(input string) (string, *sqlstate.Error, error) {
	if input == "" {
		return input, nil, nil
	}
	return path.Clean(input), nil, nil
}

func normalizeLogDestination(input string) (string, *sqlstate.Error, error) {
	list, err := ParseIdentifierList("log_destination", input)
	if err != nil {
		return "", nil, err
	}
	for _, token := range list {
		if !developerKeyword(token, "stderr") && !developerKeyword(token, "csvlog") && !developerKeyword(token, "jsonlog") && !developerKeyword(token, "syslog") {
			return developerInvalid("log_destination", input, fmt.Sprintf(`Unrecognized key word: "%s".`, token))
		}
	}
	return input, nil, nil
}

func normalizeLogConnections(input string) (string, *sqlstate.Error, error) {
	list, err := ParseIdentifierList("log_connections", input)
	if err != nil {
		return developerInvalid("log_connections", input, `Invalid list syntax in parameter "log_connections".`)
	}
	for i, token := range list {
		if i == 0 {
			for _, alias := range [...]string{"on", "true", "yes", "1", "off", "false", "no", "0"} {
				if developerKeyword(token, alias) {
					if len(list) != 1 {
						return developerInvalid("log_connections", input, fmt.Sprintf(`Cannot specify log_connections option "%s" in a list with other options.`, token))
					}
					return input, nil, nil
				}
			}
		}
		if !developerKeyword(token, "receipt") && !developerKeyword(token, "authentication") && !developerKeyword(token, "authorization") && !developerKeyword(token, "setup_durations") && !developerKeyword(token, "all") {
			return developerInvalid("log_connections", input, fmt.Sprintf(`Invalid option "%s".`, token))
		}
	}
	return input, nil, nil
}

func normalizeLogTimezone(input string) (string, *sqlstate.Error, error) {
	zone, err := sqltype.ResolveZone(input)
	if err != nil {
		return developerInvalid("log_timezone", input, "")
	}
	// pg_tz_acceptable probes tm_sec at 2000-01-01. The supported archive
	// and POSIX domain are leap-free, so this is precisely offset modulo 60.
	// Revisit the predicate if the archive ever acquires leap corrections.
	east, _ := zone.OffsetAt(946684800)
	if east%60 != 0 {
		return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`time zone "%s" appears to use leap seconds`, input), Detail: "PostgreSQL does not support leap seconds."}
	}
	return zone.Name(), nil, nil
}
