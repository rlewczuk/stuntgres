package settings

import (
	"strings"

	"stuntgres/internal/sqlstate"
)

const (
	walSettings = "Write-Ahead Log / Settings"
	walCheckpoints = "Write-Ahead Log / Checkpoints"
	walArchiving = "Write-Ahead Log / Archiving"
	walRecovery = "Write-Ahead Log / Recovery"
	walArchiveRecovery = "Write-Ahead Log / Archive Recovery"
	walRecoveryTarget = "Write-Ahead Log / Recovery Target"
	walSummarization = "Write-Ahead Log / Summarization"
)

// These released Linux-profile settings configure session values only. They do
// not install physical WAL, recovery, archiving or durability consumers.
var walBuiltins = []Definition{
	{Name: "archive_cleanup_command", Type: String, Access: Sighup, Category: walArchiveRecovery, Description: "Sets the shell command that will be executed at every restart point."},
	{Name: "archive_command", Type: String, Access: Sighup, Category: walArchiving, Description: "Sets the shell command that will be called to archive a WAL file.", ExtraDescription: "An empty string means use \"archive_library\"."},
	{Name: "archive_library", Type: String, Access: Sighup, Category: walArchiving, Description: "Sets the library that will be called to archive a WAL file.", ExtraDescription: "An empty string means use \"archive_command\"."},
	{Name: "archive_mode", Type: Enum, Access: StartupOnly, BootDefault: "off", EnumValues: []string{"always", "on", "off"}, Normalize: walEnum("archive_mode", "always,on,off"), Category: walArchiving, Description: "Allows archiving of WAL files using \"archive_command\"."},
	{Name: "archive_timeout", Type: Integer, Access: Sighup, BootDefault: "0", Unit: Seconds, IntegerMax: 1073741823, Category: walArchiving, Description: "Sets the amount of time to wait before forcing a switch to the next WAL file.", ExtraDescription: "0 disables the timeout."},
	{Name: "checkpoint_completion_target", Type: Floating, Access: Sighup, BootDefault: "0.9", FloatMax: 1, Category: walCheckpoints, Description: "Time spent flushing dirty buffers during checkpoint, as fraction of checkpoint interval."},
	{Name: "checkpoint_flush_after", Type: Integer, Access: Sighup, BootDefault: "32", Unit: Blocks8kB, IntegerMax: 256, Category: walCheckpoints, Description: "Number of pages after which previously performed writes are flushed to disk.", ExtraDescription: "0 disables forced writeback."},
	{Name: "checkpoint_timeout", Type: Integer, Access: Sighup, BootDefault: "300", Unit: Seconds, IntegerMin: 30, IntegerMax: 86400, Category: walCheckpoints, Description: "Sets the maximum time between automatic WAL checkpoints."},
	{Name: "checkpoint_warning", Type: Integer, Access: Sighup, BootDefault: "30", Unit: Seconds, IntegerMax: 2147483647, Category: walCheckpoints, Description: "Sets the maximum time before warning if checkpoints triggered by WAL volume happen too frequently.", ExtraDescription: "Write a message to the server log if checkpoints caused by the filling of WAL segment files happen more frequently than this amount of time. 0 disables the warning."},
	{Name: "commit_delay", Type: Integer, Access: Privileged, BootDefault: "0", IntegerMax: 100000, Category: walSettings, Description: "Sets the delay in microseconds between transaction commit and flushing WAL to disk."},
	{Name: "commit_siblings", Type: Integer, Access: Session, BootDefault: "5", IntegerMax: 1000, Category: walSettings, Description: "Sets the minimum number of concurrent open transactions required before performing \"commit_delay\"."},
	{Name: "fsync", Type: Boolean, Access: Sighup, BootDefault: "on", Category: walSettings, Description: "Forces synchronization of updates to disk.", ExtraDescription: "The server will use the fsync() system call in several places to make sure that updates are physically written to disk. This ensures that a database cluster will recover to a consistent state after an operating system or hardware crash."},
	{Name: "full_page_writes", Type: Boolean, Access: Sighup, BootDefault: "on", Category: walSettings, Description: "Writes full pages to WAL when first modified after a checkpoint.", ExtraDescription: "A page write in process during an operating system crash might be only partially written to disk.  During recovery, the row changes stored in WAL are not enough to recover.  This option writes pages when first modified after a checkpoint to WAL so full recovery is possible."},
	{Name: "max_wal_size", Type: Integer, Access: Sighup, BootDefault: "1024", Unit: Megabytes, IntegerMin: 2, IntegerMax: 2147483647, Category: walCheckpoints, Description: "Sets the WAL size that triggers a checkpoint."},
	{Name: "min_wal_size", Type: Integer, Access: Sighup, BootDefault: "80", Unit: Megabytes, IntegerMin: 2, IntegerMax: 2147483647, Category: walCheckpoints, Description: "Sets the minimum size to shrink the WAL to."},
	{Name: "recovery_end_command", Type: String, Access: Sighup, Category: walArchiveRecovery, Description: "Sets the shell command that will be executed once at the end of recovery."},
	{Name: "recovery_prefetch", Type: Enum, Access: Sighup, BootDefault: "try", EnumValues: []string{"off", "on", "try"}, Normalize: walEnum("recovery_prefetch", "off,on,try"), Category: walRecovery, Description: "Prefetch referenced blocks during recovery.", ExtraDescription: "Look ahead in the WAL to find references to uncached data."},
	{Name: "recovery_target", Type: String, Access: StartupOnly, Category: walRecoveryTarget, Description: "Set to \"immediate\" to end recovery as soon as a consistent state is reached."},
	{Name: "recovery_target_action", Type: Enum, Access: StartupOnly, BootDefault: "pause", EnumValues: []string{"pause", "promote", "shutdown"}, Category: walRecoveryTarget, Description: "Sets the action to perform upon reaching the recovery target."},
	{Name: "recovery_target_inclusive", Type: Boolean, Access: StartupOnly, BootDefault: "on", Category: walRecoveryTarget, Description: "Sets whether to include or exclude transaction with recovery target."},
	{Name: "recovery_target_lsn", Type: String, Access: StartupOnly, Category: walRecoveryTarget, Description: "Sets the LSN of the write-ahead log location up to which recovery will proceed."},
	{Name: "recovery_target_name", Type: String, Access: StartupOnly, Category: walRecoveryTarget, Description: "Sets the named restore point up to which recovery will proceed."},
	{Name: "recovery_target_time", Type: String, Access: StartupOnly, Category: walRecoveryTarget, Description: "Sets the time stamp up to which recovery will proceed."},
	{Name: "recovery_target_timeline", Type: String, Access: StartupOnly, BootDefault: "latest", Category: walRecoveryTarget, Description: "Specifies the timeline to recover into."},
	{Name: "recovery_target_xid", Type: String, Access: StartupOnly, Category: walRecoveryTarget, Description: "Sets the transaction ID up to which recovery will proceed."},
	{Name: "restore_command", Type: String, Access: Sighup, Category: walArchiveRecovery, Description: "Sets the shell command that will be called to retrieve an archived WAL file."},
	{Name: "summarize_wal", Type: Boolean, Access: Sighup, BootDefault: "off", Category: walSummarization, Description: "Starts the WAL summarizer process to enable incremental backup."},
	{Name: "synchronous_commit", Type: Enum, Access: Session, BootDefault: "on", EnumValues: []string{"local", "remote_write", "remote_apply", "on", "off"}, Normalize: walEnum("synchronous_commit", "local,remote_write,remote_apply,on,off"), Category: walSettings, Description: "Sets the current transaction's synchronization level."},
	{Name: "wal_buffers", Type: Integer, Access: StartupOnly, BootDefault: "-1", NativeStartup: &StartupDefault{Value: "512", Source: "default"}, Unit: Blocks8kB, IntegerMin: -1, IntegerMax: 262143, Category: walSettings, Description: "Sets the number of disk-page buffers in shared memory for WAL.", ExtraDescription: "-1 means use a fraction of \"shared_buffers\"."},
	{Name: "wal_compression", Type: Enum, Access: Privileged, BootDefault: "off", EnumValues: []string{"pglz", "lz4", "zstd", "on", "off"}, Normalize: walEnum("wal_compression", "pglz,lz4,zstd,on,off"), Category: walSettings, Description: "Compresses full-page writes written in WAL file with specified method."},
	{Name: "wal_decode_buffer_size", Type: Integer, Access: StartupOnly, BootDefault: "524288", Unit: Bytes, IntegerMin: 65536, IntegerMax: 1073741823, Category: walRecovery, Description: "Buffer size for reading ahead in the WAL during recovery.", ExtraDescription: "Maximum distance to read ahead in the WAL to prefetch referenced data blocks."},
	{Name: "wal_init_zero", Type: Boolean, Access: Privileged, BootDefault: "on", Category: walSettings, Description: "Writes zeroes to new WAL files before first use."},
	{Name: "wal_level", Type: Enum, Access: StartupOnly, BootDefault: "replica", EnumValues: []string{"minimal", "replica", "logical"}, Normalize: walEnum("wal_level", "minimal,replica,logical"), Category: walSettings, Description: "Sets the level of information written to the WAL."},
	{Name: "wal_log_hints", Type: Boolean, Access: StartupOnly, BootDefault: "off", Category: walSettings, Description: "Writes full pages to WAL when first modified after a checkpoint, even for a non-critical modification."},
	{Name: "wal_recycle", Type: Boolean, Access: Privileged, BootDefault: "on", Category: walSettings, Description: "Recycles WAL files by renaming them."},
	{Name: "wal_skip_threshold", Type: Integer, Access: Session, BootDefault: "2048", Unit: Kilobytes, IntegerMax: 2147483647, Category: walSettings, Description: "Minimum size of new file to fsync instead of writing WAL."},
	{Name: "wal_summary_keep_time", Type: Integer, Access: Sighup, BootDefault: "14400", Unit: Minutes, IntegerMax: 35791394, Category: walSummarization, Description: "Time for which WAL summary files should be kept.", ExtraDescription: "0 disables automatic summary file deletion."},
	{Name: "wal_sync_method", Type: Enum, Access: Sighup, BootDefault: "fdatasync", EnumValues: []string{"fsync", "fdatasync", "open_sync", "open_datasync"}, Category: walSettings, Description: "Selects the method used for forcing WAL updates to disk."},
	{Name: "wal_writer_delay", Type: Integer, Access: Sighup, BootDefault: "200", Unit: Milliseconds, IntegerMin: 1, IntegerMax: 10000, Category: walSettings, Description: "Time between WAL flushes performed in the WAL writer."},
	{Name: "wal_writer_flush_after", Type: Integer, Access: Sighup, BootDefault: "128", Unit: Blocks8kB, IntegerMax: 2147483647, Category: walSettings, Description: "Amount of WAL written out by WAL writer that triggers a flush."},
}

// Enum aliases are whole words, unlike Boolean GUC prefixes. Keep aliases out
// of enumvals and preserve the released ordering in diagnostics.
func walEnum(name, words string) func(string) (string, *sqlstate.Error, error) {
	values := strings.Split(words, ",")
	return func(input string) (string, *sqlstate.Error, error) {
		key := FoldName(input)
		switch name {
		case "wal_level":
			if key == "archive" || key == "hot_standby" { return "replica", nil, nil }
		default:
			switch key {
			case "true", "yes", "1": key = "on"
			case "false", "no", "0": key = "off"
			}
		}
		if name == "wal_compression" && key == "on" { key = "pglz" }
		for _, value := range values { if key == value { return value, nil, nil } }
		return "", nil, (Definition{Name: name}).invalid(input, "Available values: "+strings.Join(values, ", ")+".")
	}
}
