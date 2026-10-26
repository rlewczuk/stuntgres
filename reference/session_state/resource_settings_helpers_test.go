package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent REL_18_6 expectations, in pg_settings base units. These are not
// imported from the implementation. Initialized values deliberately differ
// from compiled boot values for the automatic buffers, stack and IO ceiling.
type resourceSetting struct {
	name, context, unit, category, kind, boot, raw, show, min, max, enums, description, extra string
}

var resourceSettings = []resourceSetting{
	{"autovacuum_work_mem", "sighup", "kB", "Memory", "integer", "-1", "-1", "-1", "-1", "2147483647", "", "Sets the maximum memory to be used by each autovacuum worker process.", `-1 means use "maintenance_work_mem".`},
	{"backend_flush_after", "user", "8kB", "I/O", "integer", "0", "0", "0", "0", "256", "", "Number of pages after which previously performed writes are flushed to disk.", "0 disables forced writeback."},
	{"bgwriter_delay", "sighup", "ms", "Background Writer", "integer", "200", "200", "200ms", "10", "10000", "", "Background writer sleep time between rounds.", ""},
	{"bgwriter_flush_after", "sighup", "8kB", "Background Writer", "integer", "64", "64", "512kB", "0", "256", "", "Number of pages after which previously performed writes are flushed to disk.", "0 disables forced writeback."},
	{"bgwriter_lru_maxpages", "sighup", "", "Background Writer", "integer", "100", "100", "100", "0", "1073741823", "", "Background writer maximum number of LRU pages to flush per round.", "0 disables background writing."},
	{"bgwriter_lru_multiplier", "sighup", "", "Background Writer", "real", "2", "2", "2", "0", "10", "", "Multiple of the average buffer usage to free per round.", ""},
	{"commit_timestamp_buffers", "postmaster", "8kB", "Memory", "integer", "0", "32", "256kB", "0", "131072", "", "Sets the size of the dedicated buffer pool used for the commit timestamp cache.", `0 means use a fraction of "shared_buffers".`},
	{"dynamic_shared_memory_type", "postmaster", "", "Memory", "enum", "posix", "posix", "posix", "", "", "{posix,sysv,mmap}", "Selects the dynamic shared memory implementation used.", ""},
	{"effective_io_concurrency", "user", "", "I/O", "integer", "16", "16", "16", "0", "1000", "", "Number of simultaneous requests that can be handled efficiently by the disk subsystem.", "0 disables simultaneous requests."},
	{"file_copy_method", "user", "", "Disk", "enum", "copy", "copy", "copy", "", "", "{copy,clone}", "Selects the file copy method.", ""},
	{"file_extend_method", "sighup", "", "Disk", "enum", "posix_fallocate", "posix_fallocate", "posix_fallocate", "", "", "{posix_fallocate,write_zeros}", "Selects the method used for extending data files.", ""},
	{"hash_mem_multiplier", "user", "", "Memory", "real", "2", "2", "2", "1", "1000", "", `Multiple of "work_mem" to use for hash tables.`, ""},
	{"huge_page_size", "postmaster", "kB", "Memory", "integer", "0", "0", "0", "0", "2147483647", "", "The size of huge page that should be requested.", "0 means use the system default."},
	{"huge_pages", "postmaster", "", "Memory", "enum", "try", "try", "try", "", "", "{off,on,try}", "Use of huge pages on Linux or Windows.", ""},
	{"io_combine_limit", "user", "8kB", "I/O", "integer", "16", "16", "128kB", "1", "128", "", "Limit on the size of data reads and writes.", ""},
	{"io_max_combine_limit", "postmaster", "8kB", "I/O", "integer", "16", "16", "128kB", "1", "128", "", "Server-wide limit that clamps io_combine_limit.", ""},
	{"io_max_concurrency", "postmaster", "", "I/O", "integer", "-1", "64", "64", "-1", "1024", "", "Max number of IOs that one process can execute simultaneously.", ""},
	{"io_method", "postmaster", "", "I/O", "enum", "worker", "worker", "worker", "", "", "{sync,worker}", "Selects the method for executing asynchronous I/O.", ""},
	{"io_workers", "sighup", "", "I/O", "integer", "3", "3", "3", "1", "32", "", "Number of IO worker processes, for io_method=worker.", ""},
	{"logical_decoding_work_mem", "user", "kB", "Memory", "integer", "65536", "65536", "64MB", "64", "2147483647", "", "Sets the maximum memory to be used for logical decoding.", "This much memory can be used by each internal reorder buffer before spilling to disk."},
	{"maintenance_io_concurrency", "user", "", "I/O", "integer", "16", "16", "16", "0", "1000", "", `A variant of "effective_io_concurrency" that is used for maintenance work.`, "0 disables simultaneous requests."},
	{"maintenance_work_mem", "user", "kB", "Memory", "integer", "65536", "65536", "64MB", "64", "2147483647", "", "Sets the maximum memory to be used for maintenance operations.", "This includes operations such as VACUUM and CREATE INDEX."},
	{"max_files_per_process", "postmaster", "", "Kernel Resources", "integer", "1000", "1000", "1000", "64", "2147483647", "", "Sets the maximum number of files each server process is allowed to open simultaneously.", ""},
	{"max_notify_queue_pages", "postmaster", "", "Disk", "integer", "1048576", "1048576", "1048576", "64", "2147483647", "", "Sets the maximum number of allocated pages for NOTIFY / LISTEN queue.", ""},
	{"max_parallel_maintenance_workers", "user", "", "Worker Processes", "integer", "2", "2", "2", "0", "1024", "", "Sets the maximum number of parallel processes per maintenance operation.", ""},
	{"max_parallel_workers", "user", "", "Worker Processes", "integer", "8", "8", "8", "0", "1024", "", "Sets the maximum number of parallel workers that can be active at one time.", ""},
	{"max_parallel_workers_per_gather", "user", "", "Worker Processes", "integer", "2", "2", "2", "0", "1024", "", "Sets the maximum number of parallel processes per executor node.", ""},
	{"max_prepared_transactions", "postmaster", "", "Memory", "integer", "0", "0", "0", "0", "262143", "", "Sets the maximum number of simultaneously prepared transactions.", ""},
	{"max_stack_depth", "superuser", "kB", "Memory", "integer", "100", "2048", "2MB", "100", "2147483647", "", "Sets the maximum stack depth, in kilobytes.", ""},
	{"max_worker_processes", "postmaster", "", "Worker Processes", "integer", "8", "8", "8", "0", "262143", "", "Maximum number of concurrent worker processes.", ""},
	{"min_dynamic_shared_memory", "postmaster", "MB", "Memory", "integer", "0", "0", "0", "0", "2147483647", "", "Amount of dynamic shared memory reserved at startup.", ""},
	{"multixact_member_buffers", "postmaster", "8kB", "Memory", "integer", "32", "32", "256kB", "16", "131072", "", "Sets the size of the dedicated buffer pool used for the MultiXact member cache.", ""},
	{"multixact_offset_buffers", "postmaster", "8kB", "Memory", "integer", "16", "16", "128kB", "16", "131072", "", "Sets the size of the dedicated buffer pool used for the MultiXact offset cache.", ""},
	{"notify_buffers", "postmaster", "8kB", "Memory", "integer", "16", "16", "128kB", "16", "131072", "", "Sets the size of the dedicated buffer pool used for the LISTEN/NOTIFY message cache.", ""},
	{"parallel_leader_participation", "user", "", "Worker Processes", "bool", "on", "on", "on", "", "", "", "Controls whether Gather and Gather Merge also run subplans.", "Should gather nodes also run subplans or just gather tuples?"},
	{"serializable_buffers", "postmaster", "8kB", "Memory", "integer", "32", "32", "256kB", "16", "131072", "", "Sets the size of the dedicated buffer pool used for the serializable transaction cache.", ""},
	{"shared_buffers", "postmaster", "8kB", "Memory", "integer", "16384", "16384", "128MB", "16", "1073741823", "", "Sets the number of shared memory buffers used by the server.", ""},
	{"shared_memory_type", "postmaster", "", "Memory", "enum", "mmap", "mmap", "mmap", "", "", "{sysv,mmap}", "Selects the shared memory implementation used for the main shared memory region.", ""},
	{"subtransaction_buffers", "postmaster", "8kB", "Memory", "integer", "0", "32", "256kB", "0", "131072", "", "Sets the size of the dedicated buffer pool used for the subtransaction cache.", `0 means use a fraction of "shared_buffers".`},
	{"temp_buffers", "user", "8kB", "Memory", "integer", "1024", "1024", "8MB", "100", "1073741823", "", "Sets the maximum number of temporary buffers used by each session.", ""},
	{"temp_file_limit", "superuser", "kB", "Disk", "integer", "-1", "-1", "-1", "-1", "2147483647", "", "Limits the total size of all temporary files used by each process.", "-1 means no limit."},
	{"transaction_buffers", "postmaster", "8kB", "Memory", "integer", "0", "32", "256kB", "0", "131072", "", "Sets the size of the dedicated buffer pool used for the transaction status cache.", `0 means use a fraction of "shared_buffers".`},
	{"vacuum_buffer_usage_limit", "user", "kB", "Memory", "integer", "2048", "2048", "2MB", "0", "16777216", "", "Sets the buffer pool size for VACUUM, ANALYZE, and autovacuum.", ""},
	{"work_mem", "user", "kB", "Memory", "integer", "4096", "4096", "4MB", "64", "2147483647", "", "Sets the maximum memory to be used for query workspaces.", "This much memory can be used by each internal sort operation and hash table before switching to temporary disk files."},
}

func resourceNullable(s string) any { if s == "" { return nil }; return s }

func (s *physicalCase) resourceInitial(d resourceSetting) (string, string, string) {
	if s.pg && d.name == "max_prepared_transactions" { return "100", "100", "command line" }
	if s.pg && (d.name == "shared_buffers" || d.name == "dynamic_shared_memory_type") { return d.raw, d.show, "configuration file" }
	return d.raw, d.show, "default"
}

func (s *physicalCase) resourceValue(name, show, raw, source string, prepared bool) {
	s.t.Helper()
	for _, query := range []struct{ sql, column string }{{"SHOW "+name, strings.ToLower(name)}, {"SELECT current_setting($1)", "current_setting"}} {
		args := []any(nil)
		if query.column == "current_setting" { args = []any{name} }
		step := fixture.Select(query.sql, []string{query.column}, []string{"TEXT"}, [][]any{{show}}, args...)
		step.Prepared = prepared
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { settingsTextMetadata(t, rows, s.pg, query.column == "current_setting") }
		s.run(step)
	}
	step := fixture.Select("SELECT setting,source FROM pg_settings WHERE name=$1", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{raw, source}}, strings.ToLower(name))
	step.Prepared = prepared
	s.run(step)
}

func resourceInvalid(name, input string) fixture.Diagnostic {
	return physicalDiagnostic("22023", fmt.Sprintf("invalid value for parameter %q: %q", name, input))
}

func resourceTempError(input string) fixture.Diagnostic {
	d := physicalDiagnostic("22023", fmt.Sprintf(`invalid value for parameter "temp_buffers": %s`, input))
	d.Detail = `"temp_buffers" cannot be changed after any temporary tables have been accessed in the session.`
	return d
}

func resourcePredicate() string {
	names := make([]string, len(resourceSettings))
	for i, d := range resourceSettings { names[i] = physicalLiteral(d.name) }
	return "name IN ("+strings.Join(names, ",")+")"
}
