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
	run(fixture.Exec("DO $$BEGIN END$$", 0))
	for _, tc := range []struct {
		name, kind, context, category string
	}{
		{"application_name", "string", "user", "Reporting and Logging / What to Log"},
		{"plpgsql.check_asserts", "bool", "user", "Customized Options"},
		{"row_security", "bool", "user", behavior},
		{"track_commit_timestamp", "bool", "postmaster", "Replication / Sending Servers"},
		{"default_transaction_isolation", "enum", "user", behavior},
		{"transaction_isolation", "enum", "user", behavior},
		{"default_transaction_read_only", "bool", "user", behavior},
		{"transaction_read_only", "bool", "user", behavior},
		{"default_transaction_deferrable", "bool", "user", behavior},
		{"transaction_deferrable", "bool", "user", behavior},
		{"bytea_output", "enum", "user", behavior},
		{"TimeZone", "string", "user", locale},
		{"DateStyle", "string", "user", locale},
		{"IntervalStyle", "enum", "user", locale},
		{"timezone_abbreviations", "string", "user", locale},
		{"client_min_messages", "enum", "user", behavior},
		{"client_encoding", "string", "user", locale},
		{"lc_messages", "string", "superuser", locale},
		{"lc_monetary", "string", "user", locale},
		{"lc_numeric", "string", "user", locale},
		{"lc_time", "string", "user", locale},
		{"icu_validation_level", "enum", "user", locale},
		{"extra_float_digits", "integer", "user", locale},
		{"search_path", "string", "user", behavior},
		{"log_statement_stats", "bool", "superuser", "Statistics / Monitoring"},
		{"server_encoding", "string", "internal", "Preset Options"},
		{"standard_conforming_strings", "bool", "user", previous},
		{"quote_all_identifiers", "bool", "user", previous},
		{"statement_timeout", "integer", "user", behavior},
		{"lock_timeout", "integer", "user", behavior},
		{"max_parallel_workers_per_gather", "integer", "user", "Resource Usage / Worker Processes"},
		{"transaction_timeout", "integer", "user", behavior},
		{"idle_in_transaction_session_timeout", "integer", "user", behavior},
		{"idle_session_timeout", "integer", "user", behavior},
	} {
		run(fixture.Select("SELECT name,vartype,context,category FROM pg_settings WHERE name=$1", []string{"name", "vartype", "context", "category"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.name, tc.kind, tc.context, tc.category}}, tc.name))
		initial := statisticsSettingsNativeInitial(tc.name)
		unit, min, max, enums := statisticsSettingsFields(tc.name)
		run(fixture.Select("SELECT unit,min_val,max_val,enumvals FROM pg_settings WHERE name=$1",
			[]string{"unit", "min_val", "max_val", "enumvals"}, []string{"TEXT", "TEXT", "TEXT", "_TEXT"},
			[][]any{{unit, min, max, enums}}, tc.name))
		if !pg {
			var boot any = initial
			source := "default"
			switch tc.name {
			case "TimeZone":
				boot, source = "GMT", "override"
			case "timezone_abbreviations":
				boot, source = nil, "override"
			case "client_encoding":
				boot, source = "SQL_ASCII", "override"
			case "server_encoding":
				boot = "SQL_ASCII"
			case "lc_messages":
				boot, source = "", "override"
			}
			run(fixture.Select("SELECT "+statisticsSettingsColumnsSQL+" FROM pg_settings WHERE name=$1", statisticsSettingsColumns, statisticsSettingsTypes,
				[][]any{{tc.name, initial, unit, tc.category, tc.context, tc.kind, source, min, max, enums, boot, initial, nil, nil, false}}, tc.name))
		}
	}
	const isolation = `{serializable,"repeatable read","read committed","read uncommitted"}`
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
		for _, prepared := range []bool{false, true} {
			absent := fixture.Select("SELECT * FROM pg_settings WHERE name='settings_metadata_missing'", statisticsSettingsCustomColumns, statisticsSettingsCustomTypes, nil)
			absent.Prepared = prepared
			absent.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				statisticsSettingsCheckMetadata(t, rows, pg, statisticsSettingsCustomColumns, statisticsSettingsCustomTypes)
			}
			run(absent)
			for _, command := range []string{"SHOW settings_metadata_missing", "SET settings_metadata_missing='on'", "RESET settings_metadata_missing"} {
				s := fixture.ImplicitError(command, fixture.ControlsReferenceDiagnostic("42704", `unrecognized configuration parameter "settings_metadata_missing"`))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared && command[:4] == "SHOW" {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(absent)
			}
			for _, command := range []string{"SET metadata.placeholder='hidden'", "RESET metadata.placeholder", "RESET ALL"} {
				run(fixture.Exec(command, 0))
				s := fixture.Select("SELECT name FROM pg_settings WHERE name='metadata.placeholder'", []string{"name"}, []string{"TEXT"}, nil)
				s.Prepared = prepared
				run(s)
			}
			invalid := fixture.ImplicitError("SET standard_conforming_strings='maybe'", fixture.ControlsReferenceDiagnostic("22023", `parameter "standard_conforming_strings" requires a Boolean value`))
			invalid.Prepared, invalid.ErrorStage = prepared, "execute"
			run(invalid)
			s := fixture.Select("SELECT setting,source,boot_val,reset_val FROM pg_settings WHERE name='standard_conforming_strings'", []string{"setting", "source", "boot_val", "reset_val"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"on", "default", "on", "on"}})
			s.Prepared = prepared
			run(s)
		}
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
		} {
			var want [][]any
			if pg || statsSnapshotSetting(tc.name) {
				want = [][]any{{tc.name, tc.kind, tc.boot, tc.context}}
			}
			if pg || statsSnapshotSetting(tc.name) {
				var unit, min, max, enums any
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
				}
				switch tc.name {
				case "compute_query_id":
					category = "Statistics / Monitoring"
				}
				run(fixture.Select("SELECT unit,category,min_val,max_val,enumvals FROM pg_settings WHERE name=$1",
					[]string{"unit", "category", "min_val", "max_val", "enumvals"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "_TEXT"},
					[][]any{{unit, category, min, max, enums}}, tc.name))
			}
			run(fixture.Select("SELECT name,vartype,boot_val,context FROM pg_settings WHERE name=$1", []string{"name", "vartype", "boot_val", "context"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, want, tc.name))
			if !pg && !statsSnapshotSetting(tc.name) {
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
		run(fixture.Exec("SET stats_fetch_consistency='none'", 0))
		run(fixture.Select("SHOW stats_fetch_consistency", []string{"stats_fetch_consistency"}, []string{"TEXT"}, [][]any{{"none"}}))
		run(fixture.Exec("RESET stats_fetch_consistency", 0))
	})
}

func statisticsSettingsNativeInitial(name string) string {
	switch name {
	case "application_name":
		return ""
	case "row_security", "standard_conforming_strings", "plpgsql.check_asserts":
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

func statisticsSettingsFields(name string) (unit, min, max, enums any) {
	switch name {
	case "statement_timeout", "lock_timeout", "transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout":
		unit, min, max = "ms", "0", "2147483647"
	case "max_parallel_workers_per_gather":
		min, max = "0", "1024"
	case "extra_float_digits":
		min, max = "-15", "3"
	}
	switch name {
	case "default_transaction_isolation", "transaction_isolation":
		enums = `{serializable,"repeatable read","read committed","read uncommitted"}`
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

func statsSnapshotSetting(name string) bool {
	switch name {
	case "stats_fetch_consistency", "track_activities", "track_counts", "track_functions", "track_io_timing", "track_wal_io_timing":
		return true
	}
	return false
}
