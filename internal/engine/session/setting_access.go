package session

import (
	"fmt"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// SettingAccess checks whether d may be set at all: startup-only, sighup and
// read-only settings are refused, and privileged ones need a parameter grant.
// allowed reports current operation authority for explicitly eligible builtins.
func SettingAccess(d settings.Definition, allowed func(name string) (bool, error)) error {
	switch d.Access {
	case settings.Backend, settings.SuperuserBackend:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be set after connection start`, d.Name))
	case settings.Sighup:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed now`, d.Name))
	case settings.StartupOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed without restarting the server`, d.Name))
	case settings.ReadOnly:
		return sqlError("55P02", fmt.Sprintf(`parameter "%s" cannot be changed`, d.Name))
	case settings.Privileged:
		if parameterGrantSetting(d.Name) {
			ok, err := allowed(d.Name)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		}
		return sqlError("42501", fmt.Sprintf(`permission denied to set parameter "%s"`, d.Name))
	}
	return nil
}

// parameterGrantSetting is the single eligibility decision for privileged
// builtins. Custom privileged definitions and unlisted builtins remain deny-only.
func parameterGrantSetting(name string) bool {
	switch name {
	case "track_activities", "track_counts", "track_functions", "track_io_timing", "track_wal_io_timing":
		return true
	case "commit_delay", "wal_compression", "wal_init_zero", "wal_recycle":
		return true
	case "log_statement_stats", "log_parser_stats", "log_planner_stats", "log_executor_stats":
		return true
	case "deadlock_timeout", "output_plugin_libraries", "max_stack_depth", "temp_file_limit":
		return true
	case "lc_messages", "dynamic_library_path", "event_triggers", "extension_control_path", "session_preload_libraries", "session_replication_role", "lo_compat_privileges":
		return true
	case "allow_in_place_tablespaces", "allow_system_table_mods", "backtrace_functions", "debug_discard_caches", "ignore_checksum_failure", "jit_dump_bitcode", "wal_consistency_checking", "zero_damaged_pages":
		return true
	case "log_duration", "log_error_verbosity", "log_lock_failures", "log_lock_waits", "log_min_duration_sample", "log_min_duration_statement", "log_min_error_statement", "log_min_messages", "log_parameter_max_length", "log_replication_commands", "log_statement", "log_statement_sample_rate", "log_temp_files", "log_transaction_sample_rate", "update_process_title":
		return true
	}
	return false
}

// SettingReadAccess applies the captured effective user's restricted-read
// authority. Requested spelling belongs in the PostgreSQL diagnostic.
func SettingReadAccess(d settings.Definition, requested string, allowed bool) error {
	if d.ReadRestricted && !allowed {
		return &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501",
			Message: fmt.Sprintf(`permission denied to examine "%s"`, requested),
			Detail:  `Only roles with privileges of the "pg_read_all_settings" role may examine this parameter.`,
		}
	}
	return nil
}
