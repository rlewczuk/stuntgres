package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func statisticsSettingsBuiltinInventory(t *testing.T, pg bool, run statisticsSettingsRun) {
	t.Helper()
	const behavior = "Client Connection Defaults / Statement Behavior"
	const locale = "Client Connection Defaults / Locale and Formatting"
	const previous = "Version and Platform Compatibility / Previous PostgreSQL Versions"
	for _, tc := range []struct {
		name, kind, context, category, description string
	}{
		{"application_name", "string", "user", "Reporting and Logging / What to Log", "Sets the application name to be reported in statistics and logs."},
		{"row_security", "bool", "user", behavior, "Enables row security."},
		{"track_commit_timestamp", "bool", "postmaster", "Replication / Sending Servers", "Collects transaction commit time."},
		{"default_transaction_isolation", "enum", "user", behavior, "Sets the transaction isolation level of each new transaction."},
		{"transaction_isolation", "enum", "user", behavior, "Sets the current transaction's isolation level."},
		{"default_transaction_read_only", "bool", "user", behavior, "Sets the default read-only status of new transactions."},
		{"transaction_read_only", "bool", "user", behavior, "Sets the current transaction's read-only status."},
		{"default_transaction_deferrable", "bool", "user", behavior, "Sets the default deferrable status of new transactions."},
		{"transaction_deferrable", "bool", "user", behavior, "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
		{"bytea_output", "enum", "user", behavior, "Sets the output format for bytea."},
		{"TimeZone", "string", "user", locale, "Sets the time zone for displaying and interpreting time stamps."},
		{"DateStyle", "string", "user", locale, "Sets the display format for date and time values."},
		{"IntervalStyle", "enum", "user", locale, "Sets the display format for interval values."},
		{"timezone_abbreviations", "string", "user", locale, "Selects a file of time zone abbreviations."},
		{"client_min_messages", "enum", "user", behavior, "Sets the message levels that are sent to the client."},
		{"client_encoding", "string", "user", locale, "Sets the client's character set encoding."},
		{"lc_messages", "string", "superuser", locale, "Sets the language in which messages are displayed."},
		{"lc_monetary", "string", "user", locale, "Sets the locale for formatting monetary amounts."},
		{"lc_numeric", "string", "user", locale, "Sets the locale for formatting numbers."},
		{"lc_time", "string", "user", locale, "Sets the locale for formatting date and time values."},
		{"icu_validation_level", "enum", "user", locale, "Log level for reporting invalid ICU locale strings."},
		{"extra_float_digits", "integer", "user", locale, "Sets the number of digits displayed for floating-point values."},
		{"search_path", "string", "user", behavior, "Sets the schema search order for names that are not schema-qualified."},
		{"log_statement_stats", "bool", "superuser", "Statistics / Monitoring", "Writes cumulative performance statistics to the server log."},
		{"server_encoding", "string", "internal", "Preset Options", "Shows the server (database) character set encoding."},
		{"standard_conforming_strings", "bool", "user", previous, "Causes '...' strings to treat backslashes literally."},
		{"quote_all_identifiers", "bool", "user", previous, "When generating SQL fragments, quote all identifiers."},
		{"statement_timeout", "integer", "user", behavior, "Sets the maximum allowed duration of any statement."},
		{"lock_timeout", "integer", "user", behavior, "Sets the maximum allowed duration of any wait for a lock."},
		{"max_parallel_workers_per_gather", "integer", "user", "Resource Usage / Worker Processes", "Sets the maximum number of parallel processes per executor node."},
		{"transaction_timeout", "integer", "user", behavior, "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
		{"idle_in_transaction_session_timeout", "integer", "user", behavior, "Sets the maximum allowed idle time between queries, when in a transaction."},
		{"idle_session_timeout", "integer", "user", behavior, "Sets the maximum allowed idle time between queries, when not in a transaction."},
	} {
		run(fixture.Select("SELECT name,vartype,context,category,short_desc FROM pg_settings WHERE name=$1", []string{"name", "vartype", "context", "category", "short_desc"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.name, tc.kind, tc.context, tc.category, tc.description}}, tc.name))
		boot := statisticsSettingsNativeBoot(tc.name)
		unit, min, max, extra, enums := statisticsSettingsFields(tc.name, pg)
		run(fixture.Select("SELECT unit,min_val,max_val,extra_desc,enumvals FROM pg_settings WHERE name=$1",
			[]string{"unit", "min_val", "max_val", "extra_desc", "enumvals"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "_TEXT"},
			[][]any{{unit, min, max, extra, enums}}, tc.name))
		if !pg {
			run(fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name=$1", statisticsSettingsColumns, statisticsSettingsTypes,
				[][]any{{tc.name, boot, unit, tc.category, tc.description, extra, tc.context, tc.kind, "default", min, max, enums, boot, boot, nil, nil, false}}, tc.name))
		}
	}
	isolation := "{\"read committed\",\"read uncommitted\",\"repeatable read\",serializable}"
	if pg {
		isolation = "{serializable,\"repeatable read\",\"read committed\",\"read uncommitted\"}"
	}
	for _, tc := range []struct{ name, values string }{
		{"transaction_isolation", isolation},
		{"default_transaction_isolation", isolation},
		{"bytea_output", "{escape,hex}"},
		{"IntervalStyle", "{postgres,postgres_verbose,sql_standard,iso_8601}"},
		{"client_min_messages", "{debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"},
		{"icu_validation_level", "{disabled,debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"},
	} {
		run(fixture.Select("SELECT enumvals FROM pg_settings WHERE name=$1", []string{"enumvals"}, []string{"_TEXT"}, [][]any{{tc.values}}, tc.name))
	}
}

func TestReferenceStatisticsSettingsUnsupportedCollectors(t *testing.T) {
	statisticsSettingsCases(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, pg bool, run statisticsSettingsRun) {
		for _, tc := range []struct{ name, kind, boot, context string }{
			{"track_activities", "bool", "on", "superuser"},
			{"track_activity_query_size", "integer", "1024", "postmaster"},
			{"track_counts", "bool", "on", "superuser"},
			{"track_cost_delay_timing", "bool", "off", "superuser"},
			{"track_io_timing", "bool", "off", "superuser"},
			{"track_wal_io_timing", "bool", "off", "superuser"},
			{"track_functions", "enum", "none", "superuser"},
			{"stats_fetch_consistency", "enum", "cache", "user"},
			{"compute_query_id", "enum", "auto", "superuser"},
			{"log_parser_stats", "bool", "off", "superuser"},
			{"log_planner_stats", "bool", "off", "superuser"},
			{"log_executor_stats", "bool", "off", "superuser"},
		} {
			var want [][]any
			if pg {
				want = [][]any{{tc.name, tc.kind, tc.boot, tc.context}}
			}
			if pg {
				var unit, min, max, extra, enums any
				category := "Statistics / Cumulative Query and Index Statistics"
				switch tc.name {
				case "track_activity_query_size":
					unit, min, max = "B", "100", "1048576"
				case "track_functions":
					enums = "{none,pl,all}"
				case "stats_fetch_consistency":
					enums = "{none,cache,snapshot}"
				case "compute_query_id":
					enums = "{auto,regress,on,off}"
				case "track_activities":
					extra = "Enables the collection of information on the currently executing command of each session, along with the time at which that command began execution."
				}
				switch tc.name {
				case "compute_query_id", "log_parser_stats", "log_planner_stats", "log_executor_stats":
					category = "Statistics / Monitoring"
				}
				description := map[string]string{
					"track_activities":          "Collects information about executing commands.",
					"track_activity_query_size": "Sets the size reserved for pg_stat_activity.query, in bytes.",
					"track_counts":              "Collects statistics on database activity.",
					"track_cost_delay_timing":   "Collects timing statistics for cost-based vacuum delay.",
					"track_io_timing":           "Collects timing statistics for database I/O activity.",
					"track_wal_io_timing":       "Collects timing statistics for WAL I/O activity.",
					"track_functions":           "Collects function-level statistics on database activity.",
					"stats_fetch_consistency":   "Sets the consistency of accesses to statistics data.",
					"compute_query_id":          "Enables in-core computation of query identifiers.",
					"log_parser_stats":          "Writes parser performance statistics to the server log.",
					"log_planner_stats":         "Writes planner performance statistics to the server log.",
					"log_executor_stats":        "Writes executor performance statistics to the server log.",
				}[tc.name]
				run(fixture.Select("SELECT short_desc FROM pg_settings WHERE name=$1", []string{"short_desc"}, []string{"TEXT"}, [][]any{{description}}, tc.name))
				run(fixture.Select("SELECT unit,category,extra_desc,min_val,max_val,enumvals FROM pg_settings WHERE name=$1",
					[]string{"unit", "category", "extra_desc", "min_val", "max_val", "enumvals"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT"},
					[][]any{{unit, category, extra, min, max, enums}}, tc.name))
			}
			run(fixture.Select("SELECT name,vartype,boot_val,context FROM pg_settings WHERE name=$1", []string{"name", "vartype", "boot_val", "context"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, want, tc.name))
			if !pg {
				for _, query := range []string{"SHOW " + tc.name, "SELECT current_setting('" + tc.name + "')", "SET " + tc.name + "='on'"} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(query, fixture.ControlsReferenceDiagnostic("42704", `unrecognized configuration parameter "`+tc.name+`"`))
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
				}
			}
		}
		run(fixture.Select("SELECT name,setting,vartype,context,category FROM pg_settings WHERE name='log_statement_stats'", []string{"name", "setting", "vartype", "context", "category"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"log_statement_stats", "off", "bool", "superuser", "Statistics / Monitoring"}}))
		if pg {
			run(fixture.Exec("SET stats_fetch_consistency='none'", 0))
			run(fixture.Select("SHOW stats_fetch_consistency", []string{"stats_fetch_consistency"}, []string{"TEXT"}, [][]any{{"none"}}))
			run(fixture.Exec("RESET stats_fetch_consistency", 0))
		}
	})
}

func statisticsSettingsNativeBoot(name string) string {
	switch name {
	case "application_name":
		return ""
	case "row_security", "standard_conforming_strings":
		return "on"
	case "default_transaction_isolation", "transaction_isolation":
		return "read committed"
	case "bytea_output":
		return "hex"
	case "TimeZone":
		return "UTC"
	case "DateStyle":
		return "ISO, MDY"
	case "IntervalStyle":
		return "postgres"
	case "timezone_abbreviations":
		return "Default"
	case "client_min_messages":
		return "notice"
	case "client_encoding", "server_encoding":
		return "UTF8"
	case "lc_messages", "lc_monetary", "lc_numeric", "lc_time":
		return "C"
	case "icu_validation_level":
		return "warning"
	case "extra_float_digits":
		return "1"
	case "search_path":
		return `"$user", public`
	case "max_parallel_workers_per_gather":
		return "2"
	case "statement_timeout", "lock_timeout", "transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout":
		return "0"
	default:
		return "off"
	}
}

func statisticsSettingsFields(name string, pg bool) (unit, min, max, extra, enums any) {
	switch name {
	case "statement_timeout", "lock_timeout", "transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout":
		unit, min, max, extra = "ms", "0", "2147483647", "0 disables the timeout."
	case "max_parallel_workers_per_gather":
		min, max = "0", "1024"
	case "extra_float_digits":
		min, max = "-15", "3"
		extra = "This affects real, double precision, and geometric data types. A zero or negative parameter value is added to the standard number of digits (FLT_DIG or DBL_DIG as appropriate). Any value greater than zero selects precise output mode."
	case "row_security":
		extra = "When enabled, row security will be applied to all users."
	case "DateStyle":
		extra = "Also controls interpretation of ambiguous date inputs."
	case "lc_messages", "lc_monetary", "lc_numeric", "lc_time":
		extra = "An empty string means use the operating system setting."
	case "client_min_messages":
		extra = "Each level includes all the levels that follow it. The later the level, the fewer messages are sent."
	}
	switch name {
	case "default_transaction_isolation", "transaction_isolation":
		enums = `{"read committed","read uncommitted","repeatable read",serializable}`
		if pg {
			enums = `{serializable,"repeatable read","read committed","read uncommitted"}`
		}
	case "bytea_output":
		enums = "{escape,hex}"
	case "IntervalStyle":
		enums = "{postgres,postgres_verbose,sql_standard,iso_8601}"
	case "client_min_messages":
		enums = "{debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"
	case "icu_validation_level":
		enums = "{disabled,debug5,debug4,debug3,debug2,debug1,log,notice,warning,error}"
	}
	return
}
