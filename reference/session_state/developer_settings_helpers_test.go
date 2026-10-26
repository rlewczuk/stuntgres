package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type developerSetting struct {
	name, kind, context, initial, alternate, description, extra string
	min, max, unit, enums                                       any
}

// Literal REL_18_6 expectations, independent of native registry definitions.
var developerSettings = []developerSetting{
	{name: "allow_in_place_tablespaces", kind: "bool", context: "superuser", initial: "off", alternate: "on", description: "Allows tablespaces directly inside pg_tblspc, for testing."},
	{name: "allow_system_table_mods", kind: "bool", context: "superuser", initial: "off", alternate: "on", description: "Allows modifications of the structure of system tables."},
	{name: "backtrace_functions", kind: "string", context: "superuser", alternate: "foo,, 9bad,", description: "Log backtrace for errors in these functions."},
	{name: "debug_discard_caches", kind: "integer", context: "superuser", initial: "0", alternate: "0", min: "0", max: "0", description: "Aggressively flush system caches for debugging purposes.", extra: "0 means use normal caching behavior."},
	{name: "debug_io_direct", kind: "string", context: "postmaster", alternate: "data", description: "Use direct I/O for file access.", extra: "An empty string disables direct I/O."},
	{name: "debug_logical_replication_streaming", kind: "enum", context: "user", initial: "buffered", alternate: "immediate", enums: "{buffered,immediate}", description: "Forces immediate streaming or serialization of changes in large transactions.", extra: "On the publisher, it allows streaming or serializing each change in logical decoding. On the subscriber, it allows serialization of all changes to files and notifies the parallel apply workers to read and apply them at the end of the transaction."},
	{name: "debug_parallel_query", kind: "enum", context: "user", initial: "off", alternate: "regress", enums: "{off,on,regress}", description: "Forces the planner's use parallel query nodes.", extra: "This can be useful for testing the parallel query infrastructure by forcing the planner to generate plans that contain nodes that perform tuple communication between workers and the main process."},
	{name: "ignore_checksum_failure", kind: "bool", context: "superuser", initial: "off", alternate: "on", description: "Continues processing after a checksum failure.", extra: "Detection of a checksum failure normally causes PostgreSQL to report an error, aborting the current transaction. Setting ignore_checksum_failure to true causes the system to ignore the failure (but still report a warning), and continue processing. This behavior could cause crashes or other serious problems. Only has an effect if checksums are enabled."},
	{name: "ignore_invalid_pages", kind: "bool", context: "postmaster", initial: "off", alternate: "on", description: "Continues recovery after an invalid pages failure.", extra: "Detection of WAL records having references to invalid pages during recovery causes PostgreSQL to raise a PANIC-level error, aborting the recovery. Setting \"ignore_invalid_pages\" to true causes the system to ignore invalid page references in WAL records (but still report a warning), and continue recovery. This behavior may cause crashes, data loss, propagate or hide corruption, or other serious problems. Only has an effect during recovery or in standby mode."},
	{name: "ignore_system_indexes", kind: "bool", context: "backend", initial: "off", alternate: "on", description: "Disables reading from system indexes.", extra: "It does not prevent updating the indexes, so it is safe to use.  The worst consequence is slowness."},
	{name: "jit_debugging_support", kind: "bool", context: "superuser-backend", initial: "off", alternate: "on", description: "Register JIT-compiled functions with debugger."},
	{name: "jit_dump_bitcode", kind: "bool", context: "superuser", initial: "off", alternate: "on", description: "Write out LLVM bitcode to facilitate JIT debugging."},
	{name: "jit_expressions", kind: "bool", context: "user", initial: "on", alternate: "off", description: "Allow JIT compilation of expressions."},
	{name: "jit_profiling_support", kind: "bool", context: "superuser-backend", initial: "off", alternate: "on", description: "Register JIT-compiled functions with perf profiler."},
	{name: "jit_tuple_deforming", kind: "bool", context: "user", initial: "on", alternate: "off", description: "Allow JIT compilation of tuple deforming."},
	{name: "post_auth_delay", kind: "integer", context: "backend", initial: "0", alternate: "1", min: "0", max: "2147", unit: "s", description: "Sets the amount of time to wait after authentication on connection startup.", extra: "This allows attaching a debugger to the process."},
	{name: "pre_auth_delay", kind: "integer", context: "sighup", initial: "0", alternate: "1", min: "0", max: "60", unit: "s", description: "Sets the amount of time to wait before authentication on connection startup.", extra: "This allows attaching a debugger to the process."},
	{name: "remove_temp_files_after_crash", kind: "bool", context: "sighup", initial: "on", alternate: "off", description: "Remove temporary files after backend crash."},
	{name: "send_abort_for_crash", kind: "bool", context: "sighup", initial: "off", alternate: "on", description: "Send SIGABRT not SIGQUIT to child processes after backend crash."},
	{name: "send_abort_for_kill", kind: "bool", context: "sighup", initial: "off", alternate: "on", description: "Send SIGABRT not SIGKILL to stuck child processes."},
	{name: "trace_notify", kind: "bool", context: "user", initial: "off", alternate: "on", description: "Generates debugging output for LISTEN and NOTIFY."},
	{name: "trace_sort", kind: "bool", context: "user", initial: "off", alternate: "on", description: "Emit information about resource usage in sorting."},
	{name: "wal_consistency_checking", kind: "string", context: "superuser", alternate: "heap, btree", description: "Sets the WAL resource managers for which WAL consistency checks are done.", extra: "Full-page images will be logged for all data blocks and cross-checked against the results of WAL replay."},
	{name: "zero_damaged_pages", kind: "bool", context: "superuser", initial: "off", alternate: "on", description: "Continues processing past damaged page headers.", extra: "Detection of a damaged page header normally causes PostgreSQL to report an error, aborting the current transaction. Setting \"zero_damaged_pages\" to true causes the system to instead report a warning, zero out the damaged page, and continue processing. This behavior will destroy data, namely all the rows on the damaged page."},
}
var developerAbsent = []string{"debug_copy_parse_plan_trees", "debug_deadlocks", "debug_raw_expression_coverage_test", "debug_write_read_parse_plan_trees", "log_btree_build_stats", "trace_lock_oidmin", "trace_lock_table", "trace_locks", "trace_lwlocks", "trace_userlocks", "wal_debug"}

func developerMutable(tc developerSetting) bool {
	return tc.context == "user" || tc.context == "superuser"
}
func developerContext(tc developerSetting) fixture.Diagnostic {
	message := "cannot be set after connection start"
	if tc.context == "sighup" {
		message = "cannot be changed now"
	}
	if tc.context == "postmaster" {
		message = "cannot be changed without restarting the server"
	}
	return physicalDiagnostic("55P02", fmt.Sprintf("parameter %q %s", tc.name, message))
}
func developerDenied(name string) fixture.Diagnostic {
	return physicalDiagnostic("42501", fmt.Sprintf("permission denied to set parameter %q", name))
}
func developerUnknown(name string) fixture.Diagnostic {
	return physicalDiagnostic("42704", fmt.Sprintf("unrecognized configuration parameter %q", name))
}
func developerInvalid(tc developerSetting, value string) fixture.Diagnostic {
	d := physicalDiagnostic("22023", fmt.Sprintf("invalid value for parameter %q: %q", tc.name, value))
	switch tc.kind {
	case "bool":
		d.Message = fmt.Sprintf("parameter %q requires a Boolean value", tc.name)
	case "enum":
		if tc.name == "debug_parallel_query" {
			d.Hint = "Available values: off, on, regress."
		} else {
			d.Hint = "Available values: buffered, immediate."
		}
	case "integer":
		d.Message = fmt.Sprintf("1 is outside the valid range for parameter %q (0 .. 0)", tc.name)
	case "string":
		if tc.name == "backtrace_functions" {
			d.Detail = "Invalid character."
		} else {
			d.Detail = fmt.Sprintf("Unrecognized key word: %q.", value)
		}
	}
	return d
}
func (s *physicalCase) developerExec(query string, prepared bool) {
	step := fixture.Exec(query, 0)
	step.Prepared = prepared
	s.run(step)
}
func (s *physicalCase) developerRead(tc developerSetting, value string, prepared bool) {
	for _, query := range []string{"SHOW " + tc.name, "SELECT current_setting('" + tc.name + "')"} {
		column := "current_setting"
		if strings.HasPrefix(query, "SHOW") {
			column = tc.name
		}
		step := fixture.Select(query, []string{column}, []string{"TEXT"}, [][]any{{value}})
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			settingsTextMetadata(t, rows, s.pg, column == "current_setting")
		}
		s.run(step)
	}
}
func (s *physicalCase) developerMetadata(tc developerSetting, value, source string, prepared bool) {
	var extra any
	if tc.extra != "" {
		extra = tc.extra
	}
	want := []any{tc.name, value, tc.unit, "Developer Options", tc.description, extra, tc.context, tc.kind, source, tc.min, tc.max, tc.enums, tc.initial, tc.initial, nil, nil, false}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='"+tc.name+"'", physicalColumns, physicalTypes, [][]any{want})
	step.Prepared = prepared
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes)
	}
	s.run(step)
}
func (s *physicalCase) developerConfig(tc developerSetting, value any, want string, prepared bool) {
	step := fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{want}}, tc.name, value, false)
	step.Prepared = prepared
	s.run(step)
}
func (s *physicalCase) developerAll(prepared bool) {
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
	got := map[string][]string{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			s.t.Fatal(err)
		}
		got[name] = []string{value, description}
	}
	if rows.Err() != nil {
		s.t.Fatal(rows.Err())
	}
	for _, tc := range developerSettings {
		if !reflect.DeepEqual(got[tc.name], []string{tc.initial, tc.description}) {
			s.t.Fatalf("SHOW ALL %s: %v", tc.name, got[tc.name])
		}
	}
	for _, name := range developerAbsent {
		if _, ok := got[name]; ok {
			s.t.Fatal("unexpected conditional setting", name)
		}
	}
}
