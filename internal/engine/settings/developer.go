package settings

import (
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

// These setting-only descriptors match the pinned release build. None has an
// operational consumer: accepting a value never enables physical mechanisms.
// Compile-conditional debug settings absent from that build are not registered.
var developerBuiltins = []Definition{
	{Name: "allow_in_place_tablespaces", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Developer Options", Description: "Allows tablespaces directly inside pg_tblspc, for testing."},
	{Name: "allow_system_table_mods", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Developer Options", Description: "Allows modifications of the structure of system tables."},
	{Name: "backtrace_functions", Type: String, Access: Privileged, Category: "Developer Options", Description: "Log backtrace for errors in these functions.", Normalize: normalizeBacktraceFunctions},
	{Name: "debug_discard_caches", Type: Integer, Access: Privileged, BootDefault: "0", Category: "Developer Options", Description: "Aggressively flush system caches for debugging purposes.", ExtraDescription: "0 means use normal caching behavior."},
	{Name: "debug_io_direct", Type: String, Access: StartupOnly, Category: "Developer Options", Description: "Use direct I/O for file access.", ExtraDescription: "An empty string disables direct I/O.", Normalize: normalizeDebugIODirect},
	{Name: "debug_logical_replication_streaming", Type: Enum, BootDefault: "buffered", EnumValues: []string{"buffered", "immediate"}, Category: "Developer Options", Description: "Forces immediate streaming or serialization of changes in large transactions.", ExtraDescription: "On the publisher, it allows streaming or serializing each change in logical decoding. On the subscriber, it allows serialization of all changes to files and notifies the parallel apply workers to read and apply them at the end of the transaction."},
	{Name: "debug_parallel_query", Type: Enum, BootDefault: "off", EnumValues: []string{"off", "on", "regress"}, Category: "Developer Options", Description: "Forces the planner's use parallel query nodes.", ExtraDescription: "This can be useful for testing the parallel query infrastructure by forcing the planner to generate plans that contain nodes that perform tuple communication between workers and the main process."},
	{Name: "ignore_checksum_failure", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Developer Options", Description: "Continues processing after a checksum failure.", ExtraDescription: "Detection of a checksum failure normally causes PostgreSQL to report an error, aborting the current transaction. Setting ignore_checksum_failure to true causes the system to ignore the failure (but still report a warning), and continue processing. This behavior could cause crashes or other serious problems. Only has an effect if checksums are enabled."},
	{Name: "ignore_invalid_pages", Type: Boolean, Access: StartupOnly, BootDefault: "off", Category: "Developer Options", Description: "Continues recovery after an invalid pages failure.", ExtraDescription: "Detection of WAL records having references to invalid pages during recovery causes PostgreSQL to raise a PANIC-level error, aborting the recovery. Setting \"ignore_invalid_pages\" to true causes the system to ignore invalid page references in WAL records (but still report a warning), and continue recovery. This behavior may cause crashes, data loss, propagate or hide corruption, or other serious problems. Only has an effect during recovery or in standby mode."},
	{Name: "ignore_system_indexes", Type: Boolean, Access: Backend, BootDefault: "off", Category: "Developer Options", Description: "Disables reading from system indexes.", ExtraDescription: "It does not prevent updating the indexes, so it is safe to use.  The worst consequence is slowness."},
	{Name: "jit_debugging_support", Type: Boolean, Access: SuperuserBackend, BootDefault: "off", Category: "Developer Options", Description: "Register JIT-compiled functions with debugger."},
	{Name: "jit_dump_bitcode", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Developer Options", Description: "Write out LLVM bitcode to facilitate JIT debugging."},
	{Name: "jit_expressions", Type: Boolean, BootDefault: "on", Category: "Developer Options", Description: "Allow JIT compilation of expressions."},
	{Name: "jit_profiling_support", Type: Boolean, Access: SuperuserBackend, BootDefault: "off", Category: "Developer Options", Description: "Register JIT-compiled functions with perf profiler."},
	{Name: "jit_tuple_deforming", Type: Boolean, BootDefault: "on", Category: "Developer Options", Description: "Allow JIT compilation of tuple deforming."},
	{Name: "post_auth_delay", Type: Integer, Access: Backend, Unit: Seconds, IntegerMax: 2147, BootDefault: "0", Category: "Developer Options", Description: "Sets the amount of time to wait after authentication on connection startup.", ExtraDescription: "This allows attaching a debugger to the process."},
	{Name: "pre_auth_delay", Type: Integer, Access: Sighup, Unit: Seconds, IntegerMax: 60, BootDefault: "0", Category: "Developer Options", Description: "Sets the amount of time to wait before authentication on connection startup.", ExtraDescription: "This allows attaching a debugger to the process."},
	{Name: "remove_temp_files_after_crash", Type: Boolean, Access: Sighup, BootDefault: "on", Category: "Developer Options", Description: "Remove temporary files after backend crash."},
	{Name: "send_abort_for_crash", Type: Boolean, Access: Sighup, BootDefault: "off", Category: "Developer Options", Description: "Send SIGABRT not SIGQUIT to child processes after backend crash."},
	{Name: "send_abort_for_kill", Type: Boolean, Access: Sighup, BootDefault: "off", Category: "Developer Options", Description: "Send SIGABRT not SIGKILL to stuck child processes."},
	{Name: "trace_notify", Type: Boolean, BootDefault: "off", Category: "Developer Options", Description: "Generates debugging output for LISTEN and NOTIFY."},
	{Name: "trace_sort", Type: Boolean, BootDefault: "off", Category: "Developer Options", Description: "Emit information about resource usage in sorting."},
	{Name: "wal_consistency_checking", Type: String, Access: Privileged, Category: "Developer Options", Description: "Sets the WAL resource managers for which WAL consistency checks are done.", ExtraDescription: "Full-page images will be logged for all data blocks and cross-checked against the results of WAL replay.", Normalize: normalizeWALConsistencyChecking},
	{Name: "zero_damaged_pages", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Developer Options", Description: "Continues processing past damaged page headers.", ExtraDescription: "Detection of a damaged page header normally causes PostgreSQL to report an error, aborting the current transaction. Setting \"zero_damaged_pages\" to true causes the system to instead report a warning, zero out the damaged page, and continue processing. This behavior will destroy data, namely all the rows on the damaged page."},
}

func developerInvalid(name, input, detail string) (string, *sqlstate.Error, error) {
	return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "%s": "%s"`, name, input), Detail: detail}
}

func normalizeBacktraceFunctions(input string) (string, *sqlstate.Error, error) {
	for i := range len(input) {
		c := input[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == ',' || c == ' ' || c == '\n' || c == '\t') {
			return developerInvalid("backtrace_functions", input, "Invalid character.")
		}
	}
	return input, nil, nil
}

func normalizeWALConsistencyChecking(input string) (string, *sqlstate.Error, error) {
	list, err := ParseIdentifierList("wal_consistency_checking", input)
	if err != nil {
		return "", nil, err
	}
	for _, token := range list {
		valid := false
		for _, name := range [...]string{"all", "heap", "heap2", "btree", "hash", "gin", "gist", "sequence", "spgist", "brin", "generic"} {
			if developerKeyword(token, name) {
				valid = true
				break
			}
		}
		if !valid {
			return developerInvalid("wal_consistency_checking", input, fmt.Sprintf(`Unrecognized key word: "%s".`, token))
		}
	}
	return input, nil, nil
}

// Builtin keywords are ASCII, including when quoted. Avoid both allocating a
// lowercase copy and Unicode folding that PostgreSQL does not perform here.
func developerKeyword(token, keyword string) bool {
	if len(token) != len(keyword) {
		return false
	}
	for i := range len(token) {
		c := token[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != keyword[i] {
			return false
		}
	}
	return true
}

// SplitGUCList differs from identifier lists: unquoted words are not folded or
// truncated. Quoted words support doubled quotes and preserve interior spaces.
func normalizeDebugIODirect(input string) (string, *sqlstate.Error, error) {
	const whitespace = " \t\r\n\f\v"
	s := strings.Trim(input, whitespace)
	invalid := func() (string, *sqlstate.Error, error) {
		return developerInvalid("debug_io_direct", input, `Invalid list syntax in parameter "debug_io_direct".`)
	}
	var unknown string
	hasUnknown := false
	for s != "" {
		var token string
		if s[0] == '"' {
			i := 1
			for {
				if i == len(s) {
					return invalid()
				}
				if s[i] == '"' {
					if i+1 < len(s) && s[i+1] == '"' {
						i += 2
						continue
					}
					break
				}
				i++
			}
			token, s = strings.ReplaceAll(s[1:i], `""`, `"`), strings.Trim(s[i+1:], whitespace)
		} else {
			i := 0
			for i < len(s) && s[i] != ',' && !strings.ContainsRune(whitespace, rune(s[i])) {
				i++
			}
			if i == 0 {
				return invalid()
			}
			token, s = s[:i], strings.Trim(s[i:], whitespace)
		}
		if s != "" && s[0] != ',' {
			return invalid()
		}
		if !hasUnknown && !developerKeyword(token, "data") && !developerKeyword(token, "wal") && !developerKeyword(token, "wal_init") {
			unknown, hasUnknown = token, true
		}
		if s != "" {
			s = strings.Trim(s[1:], whitespace)
			if s == "" {
				return invalid()
			}
		}
	}
	if hasUnknown {
		return developerInvalid("debug_io_direct", input, fmt.Sprintf(`Invalid option "%s".`, unknown))
	}
	return input, nil, nil
}
