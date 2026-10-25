package settings

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

var applicationName = Definition{
	Name: "application_name", Type: String,
	Description: "Sets the application name to be reported in statistics and logs.",
	Normalize:   normalizeApplicationName,
}
var builtins = []Definition{
	applicationName,
	{Name: "config_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/postgresql.conf"}, Description: "Sets the server's main configuration file."},
	{Name: "data_directory", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/data"}, Description: "Sets the server's data directory."},
	{Name: "external_pid_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{ResetNull: true}, Description: "Writes the postmaster PID to the specified file."},
	{Name: "hba_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/pg_hba.conf"}, Description: `Sets the server's "hba" configuration file.`},
	{Name: "ident_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/pg_ident.conf"}, Description: `Sets the server's "ident" configuration file.`},
	{Name: "row_security", Type: Boolean, BootDefault: "on", Description: "Enables row security."},
	{Name: "plpgsql.check_asserts", Type: Boolean, BootDefault: "on", Description: "Perform checks given in ASSERT statements."},
	{Name: "track_commit_timestamp", Type: Boolean, Access: StartupOnly, BootDefault: "off", Description: "Collects transaction commit time."},
	{Name: "default_transaction_isolation", Type: Enum, BootDefault: "read committed", EnumValues: []string{"read committed", "read uncommitted", "repeatable read", "serializable"}, Description: "Sets the transaction isolation level of each new transaction."},
	{Name: "transaction_isolation", Type: Enum, BootDefault: "read committed", EnumValues: []string{"read committed", "read uncommitted", "repeatable read", "serializable"}, Description: "Sets the current transaction's isolation level."},
	{Name: "default_transaction_read_only", Type: Boolean, BootDefault: "off", Description: "Sets the default read-only status of new transactions."},
	{Name: "transaction_read_only", Type: Boolean, BootDefault: "off", Description: "Sets the current transaction's read-only status."},
	{Name: "default_transaction_deferrable", Type: Boolean, BootDefault: "off", Description: "Sets the default deferrable status of new transactions."},
	{Name: "transaction_deferrable", Type: Boolean, BootDefault: "off", Description: "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
	{Name: "bytea_output", Type: Enum, BootDefault: "hex", EnumValues: []string{"escape", "hex"}, Description: "Sets the output format for bytea."},
	{Name: "timezone", Type: String, BootDefault: "UTC", Description: "Sets the time zone for displaying and interpreting time stamps."},
	{Name: "datestyle", Type: String, BootDefault: "ISO, MDY", Description: "Sets the display format for date and time values."},
	{Name: "intervalstyle", Type: Enum, BootDefault: "postgres", EnumValues: []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"}, Description: "Sets the display format for interval values."},
	{Name: "timezone_abbreviations", Type: String, BootDefault: "Default", Description: "Selects a file of time zone abbreviations."},
	ClientMinMessages,
	{Name: "client_encoding", Type: String, BootDefault: "UTF8", Description: "Sets the client's character set encoding.", Normalize: normalizeClientEncoding},
	{Name: "lc_messages", Type: String, Access: Privileged, BootDefault: "C", Description: "Sets the language in which messages are displayed.", Normalize: normalizeLCMessages},
	{Name: "lc_monetary", Type: String, BootDefault: "C", Description: "Sets the locale for formatting monetary amounts.", Normalize: normalizeLCMonetary},
	{Name: "lc_numeric", Type: String, BootDefault: "C", Description: "Sets the locale for formatting numbers.", Normalize: normalizeLCNumeric},
	{Name: "lc_time", Type: String, BootDefault: "C", Description: "Sets the locale for formatting date and time values.", Normalize: normalizeLCTime},
	icuValidationLevel,
	{Name: "extra_float_digits", Type: Integer, BootDefault: "1", IntegerMin: -15, IntegerMax: 3, Description: "Sets the number of digits displayed for floating-point values."},
	{Name: "search_path", Type: String, BootDefault: `"$user", public`, Description: "Sets the schema search order for names that are not schema-qualified."},
	{Name: "log_statement_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Description: "Writes cumulative performance statistics to the server log."},
	{Name: "server_encoding", Type: String, Access: ReadOnly, BootDefault: "UTF8", Description: "Shows the server (database) character set encoding."},
	{Name: "standard_conforming_strings", Type: Boolean, BootDefault: "on", Description: "Causes '...' strings to treat backslashes literally."},
	{Name: "quote_all_identifiers", Type: Boolean, BootDefault: "off", Description: "When generating SQL fragments, quote all identifiers."},
	{Name: "statement_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any statement."},
	{Name: "lock_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any wait for a lock."},
	{Name: "max_parallel_workers_per_gather", Type: Integer, IntegerMax: 1024, BootDefault: "2", Description: "Sets the maximum number of parallel processes per executor node."},
	{Name: "enable_async_append", Type: Boolean, BootDefault: "on", Description: "Enables the planner's use of async append plans."},
	{Name: "transaction_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
	{Name: "idle_in_transaction_session_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed idle time between queries, when in a transaction."},
	{Name: "idle_session_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed idle time between queries, when not in a transaction."},
}

func normalizeApplicationName(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	var notice *sqlstate.Error
	if len(value) > 63 {
		n := 63
		for !utf8.RuneStart(value[n]) {
			n--
		}
		notice = &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, value, value[:n])}
		value = value[:n]
	}
	printable := true
	for i := range len(value) {
		if value[i] < 32 || value[i] > 126 {
			printable = false
			break
		}
	}
	if printable {
		return value, notice, nil
	}
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := range len(value) {
		c := value[i]
		if c >= 32 && c <= 126 {
			b.WriteByte(c)
		} else {
			b.WriteString(`\x`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String(), notice, nil
}
