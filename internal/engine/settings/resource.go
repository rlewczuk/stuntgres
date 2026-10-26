package settings

import (
	"fmt"

	"stuntgres/internal/sqlstate"
)

const (
	resourceMemory = "Resource Usage / Memory"
	resourceDisk = "Resource Usage / Disk"
	resourceKernel = "Resource Usage / Kernel Resources"
	resourceWriter = "Resource Usage / Background Writer"
	resourceIO = "Resource Usage / I/O"
	resourceWorkers = "Resource Usage / Worker Processes"
)

// Resource descriptors model a fixed 64-bit Linux profile, not host allocation
// capabilities. max_parallel_workers_per_gather retains its logical consumer.
var resourceBuiltins = []Definition{
	{Name: "autovacuum_work_mem", Type: Integer, Access: Sighup, Unit: Kilobytes, BootDefault: "-1", IntegerMin: -1, IntegerMax: 2147483647, Category: resourceMemory, Description: "Sets the maximum memory to be used by each autovacuum worker process.", ExtraDescription: "-1 means use \"maintenance_work_mem\"."},
	{Name: "backend_flush_after", Type: Integer, Unit: Blocks8kB, BootDefault: "0", IntegerMax: 256, Category: resourceIO, Description: "Number of pages after which previously performed writes are flushed to disk.", ExtraDescription: "0 disables forced writeback."},
	{Name: "bgwriter_delay", Type: Integer, Access: Sighup, Unit: Milliseconds, BootDefault: "200", IntegerMin: 10, IntegerMax: 10000, Category: resourceWriter, Description: "Background writer sleep time between rounds."},
	{Name: "bgwriter_flush_after", Type: Integer, Access: Sighup, Unit: Blocks8kB, BootDefault: "64", IntegerMax: 256, Category: resourceWriter, Description: "Number of pages after which previously performed writes are flushed to disk.", ExtraDescription: "0 disables forced writeback."},
	{Name: "bgwriter_lru_maxpages", Type: Integer, Access: Sighup, BootDefault: "100", IntegerMax: 1073741823, Category: resourceWriter, Description: "Background writer maximum number of LRU pages to flush per round.", ExtraDescription: "0 disables background writing."},
	{Name: "bgwriter_lru_multiplier", Type: Floating, Access: Sighup, BootDefault: "2", FloatMax: 10, Category: resourceWriter, Description: "Multiple of the average buffer usage to free per round."},
	{Name: "commit_timestamp_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "0", NativeStartup: &StartupDefault{Value: "32", Source: "default"}, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the commit timestamp cache.", ExtraDescription: "0 means use a fraction of \"shared_buffers\"."},
	{Name: "dynamic_shared_memory_type", Type: Enum, Access: StartupOnly, BootDefault: "posix", EnumValues: []string{"posix", "sysv", "mmap"}, Category: resourceMemory, Description: "Selects the dynamic shared memory implementation used."},
	{Name: "effective_io_concurrency", Type: Integer, BootDefault: "16", IntegerMax: 1000, Category: resourceIO, Description: "Number of simultaneous requests that can be handled efficiently by the disk subsystem.", ExtraDescription: "0 disables simultaneous requests."},
	{Name: "file_copy_method", Type: Enum, BootDefault: "copy", EnumValues: []string{"copy", "clone"}, Category: resourceDisk, Description: "Selects the file copy method."},
	{Name: "file_extend_method", Type: Enum, Access: Sighup, BootDefault: "posix_fallocate", EnumValues: []string{"posix_fallocate", "write_zeros"}, Category: resourceDisk, Description: "Selects the method used for extending data files."},
	{Name: "hash_mem_multiplier", Type: Floating, BootDefault: "2", FloatMin: 1, FloatMax: 1000, Category: resourceMemory, Description: "Multiple of \"work_mem\" to use for hash tables."},
	{Name: "huge_page_size", Type: Integer, Access: StartupOnly, Unit: Kilobytes, BootDefault: "0", IntegerMax: 2147483647, Category: resourceMemory, Description: "The size of huge page that should be requested.", ExtraDescription: "0 means use the system default."},
	{Name: "huge_pages", Type: Enum, Access: StartupOnly, BootDefault: "try", EnumValues: []string{"off", "on", "try"}, Category: resourceMemory, Description: "Use of huge pages on Linux or Windows."},
	{Name: "io_combine_limit", Type: Integer, Unit: Blocks8kB, BootDefault: "16", IntegerMin: 1, IntegerMax: 128, Category: resourceIO, Description: "Limit on the size of data reads and writes."},
	{Name: "io_max_combine_limit", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "16", IntegerMin: 1, IntegerMax: 128, Category: resourceIO, Description: "Server-wide limit that clamps io_combine_limit."},
	{Name: "io_max_concurrency", Type: Integer, Access: StartupOnly, BootDefault: "-1", NativeStartup: &StartupDefault{Value: "64", Source: "default"}, IntegerMin: -1, IntegerMax: 1024, Category: resourceIO, Description: "Max number of IOs that one process can execute simultaneously."},
	{Name: "io_method", Type: Enum, Access: StartupOnly, BootDefault: "worker", EnumValues: []string{"sync", "worker"}, Category: resourceIO, Description: "Selects the method for executing asynchronous I/O."},
	{Name: "io_workers", Type: Integer, Access: Sighup, BootDefault: "3", IntegerMin: 1, IntegerMax: 32, Category: resourceIO, Description: "Number of IO worker processes, for io_method=worker."},
	{Name: "logical_decoding_work_mem", Type: Integer, Unit: Kilobytes, BootDefault: "65536", IntegerMin: 64, IntegerMax: 2147483647, Category: resourceMemory, Description: "Sets the maximum memory to be used for logical decoding.", ExtraDescription: "This much memory can be used by each internal reorder buffer before spilling to disk."},
	{Name: "maintenance_io_concurrency", Type: Integer, BootDefault: "16", IntegerMax: 1000, Category: resourceIO, Description: "A variant of \"effective_io_concurrency\" that is used for maintenance work.", ExtraDescription: "0 disables simultaneous requests."},
	{Name: "maintenance_work_mem", Type: Integer, Unit: Kilobytes, BootDefault: "65536", IntegerMin: 64, IntegerMax: 2147483647, Category: resourceMemory, Description: "Sets the maximum memory to be used for maintenance operations.", ExtraDescription: "This includes operations such as VACUUM and CREATE INDEX."},
	{Name: "max_files_per_process", Type: Integer, Access: StartupOnly, BootDefault: "1000", IntegerMin: 64, IntegerMax: 2147483647, Category: resourceKernel, Description: "Sets the maximum number of files each server process is allowed to open simultaneously."},
	{Name: "max_notify_queue_pages", Type: Integer, Access: StartupOnly, BootDefault: "1048576", IntegerMin: 64, IntegerMax: 2147483647, Category: resourceDisk, Description: "Sets the maximum number of allocated pages for NOTIFY / LISTEN queue."},
	{Name: "max_parallel_maintenance_workers", Type: Integer, BootDefault: "2", IntegerMax: 1024, Category: resourceWorkers, Description: "Sets the maximum number of parallel processes per maintenance operation."},
	{Name: "max_parallel_workers", Type: Integer, BootDefault: "8", IntegerMax: 1024, Category: resourceWorkers, Description: "Sets the maximum number of parallel workers that can be active at one time."},
	{Name: "max_prepared_transactions", Type: Integer, Access: StartupOnly, BootDefault: "0", IntegerMax: 262143, Category: resourceMemory, Description: "Sets the maximum number of simultaneously prepared transactions."},
	{Name: "max_stack_depth", Type: Integer, Access: Privileged, Unit: Kilobytes, BootDefault: "100", NativeStartup: &StartupDefault{Value: "2048", Source: "default"}, IntegerMin: 100, IntegerMax: 2147483647, Category: resourceMemory, Description: "Sets the maximum stack depth, in kilobytes."},
	{Name: "max_worker_processes", Type: Integer, Access: StartupOnly, BootDefault: "8", IntegerMax: 262143, Category: resourceWorkers, Description: "Maximum number of concurrent worker processes."},
	{Name: "min_dynamic_shared_memory", Type: Integer, Access: StartupOnly, Unit: Megabytes, BootDefault: "0", IntegerMax: 2147483647, Category: resourceMemory, Description: "Amount of dynamic shared memory reserved at startup."},
	{Name: "multixact_member_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "32", IntegerMin: 16, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the MultiXact member cache."},
	{Name: "multixact_offset_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "16", IntegerMin: 16, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the MultiXact offset cache."},
	{Name: "notify_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "16", IntegerMin: 16, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the LISTEN/NOTIFY message cache."},
	{Name: "parallel_leader_participation", Type: Boolean, BootDefault: "on", Category: resourceWorkers, Description: "Controls whether Gather and Gather Merge also run subplans.", ExtraDescription: "Should gather nodes also run subplans or just gather tuples?"},
	{Name: "serializable_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "32", IntegerMin: 16, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the serializable transaction cache."},
	{Name: "shared_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "16384", IntegerMin: 16, IntegerMax: 1073741823, Category: resourceMemory, Description: "Sets the number of shared memory buffers used by the server."},
	{Name: "shared_memory_type", Type: Enum, Access: StartupOnly, BootDefault: "mmap", EnumValues: []string{"sysv", "mmap"}, Category: resourceMemory, Description: "Selects the shared memory implementation used for the main shared memory region."},
	{Name: "subtransaction_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "0", NativeStartup: &StartupDefault{Value: "32", Source: "default"}, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the subtransaction cache.", ExtraDescription: "0 means use a fraction of \"shared_buffers\"."},
	{Name: "temp_buffers", Type: Integer, Unit: Blocks8kB, BootDefault: "1024", IntegerMin: 100, IntegerMax: 1073741823, Category: resourceMemory, Description: "Sets the maximum number of temporary buffers used by each session."},
	{Name: "temp_file_limit", Type: Integer, Access: Privileged, Unit: Kilobytes, BootDefault: "-1", IntegerMin: -1, IntegerMax: 2147483647, Category: resourceDisk, Description: "Limits the total size of all temporary files used by each process.", ExtraDescription: "-1 means no limit."},
	{Name: "transaction_buffers", Type: Integer, Access: StartupOnly, Unit: Blocks8kB, BootDefault: "0", NativeStartup: &StartupDefault{Value: "32", Source: "default"}, IntegerMax: 131072, Category: resourceMemory, Description: "Sets the size of the dedicated buffer pool used for the transaction status cache.", ExtraDescription: "0 means use a fraction of \"shared_buffers\"."},
	{Name: "vacuum_buffer_usage_limit", Type: Integer, Unit: Kilobytes, BootDefault: "2048", IntegerMax: 16777216, Category: resourceMemory, Description: "Sets the buffer pool size for VACUUM, ANALYZE, and autovacuum."},
	{Name: "work_mem", Type: Integer, Unit: Kilobytes, BootDefault: "4096", IntegerMin: 64, IntegerMax: 2147483647, Category: resourceMemory, Description: "Sets the maximum memory to be used for query workspaces.", ExtraDescription: "This much memory can be used by each internal sort operation and hash table before switching to temporary disk files."},
}

// resourceNumber applies released check hooks after ordinary type/unit/bounds
// validation. No check observes or changes physical host resources.
func (d Definition) resourceNumber(number float64) (float64, error) {
	detail, hint := "", ""
	switch d.Name {
	case "autovacuum_work_mem":
		if number >= 0 && number < 64 { number = 64 }
	case "vacuum_buffer_usage_limit":
		if number > 0 && number < 128 { detail = `"vacuum_buffer_usage_limit" must be 0 or between 128 kB and 16777216 kB.` }
	case "max_stack_depth":
		if number > 7680 {
			detail = `"max_stack_depth" must not exceed 7680kB.`
			hint = `Increase the platform's stack depth limit via "ulimit -s" or local equivalent.`
		}
	case "io_max_concurrency":
		if number == 0 { detail = "Only -1 or values bigger than 0 are valid." }
	case "commit_timestamp_buffers", "multixact_member_buffers", "multixact_offset_buffers", "notify_buffers", "serializable_buffers", "subtransaction_buffers", "transaction_buffers":
		if int64(number)%16 != 0 { detail = fmt.Sprintf(`"%s" must be a multiple of 16.`, d.Name) }
	}
	if detail != "" {
		return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "%s": %d`, d.Name, int64(number)), Detail: detail, Hint: hint}
	}
	return number, nil
}
