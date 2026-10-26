package settings

import "math"

// Physical presets describe immutable simulations, not allocated resources.
// Keep PostgreSQL descriptor text separate from the documented native boundary.
var presetBuiltins = func() []Definition {
	definitions := []Definition{
		{Name: "block_size", Type: Integer, BootDefault: "8192", IntegerMin: 8192, IntegerMax: 8192, Description: "Shows the size of a disk block."},
		{Name: "data_checksums", Type: Boolean, BootDefault: "off", Description: "Shows whether data checksums are turned on for this cluster."},
		{Name: "data_directory_mode", Type: Integer, BootDefault: "448", IntegerMax: 511, Description: "Shows the mode of the data directory.", ExtraDescription: "The parameter value is a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)"},
		{Name: "debug_assertions", Type: Boolean, BootDefault: "off", Description: "Shows whether the running server has assertion checks enabled."},
		{Name: "huge_pages_status", Type: Enum, BootDefault: "unknown", NativeStartup: &StartupDefault{Value: "off", Source: "default"}, EnumValues: []string{"off", "on", "unknown"}, Description: "Indicates the status of huge pages."},
		{Name: "in_hot_standby", Type: Boolean, BootDefault: "off", Description: "Shows whether hot standby is currently active."},
		{Name: "integer_datetimes", Type: Boolean, BootDefault: "on", Description: "Shows whether datetimes are integer based."},
		{Name: "max_function_args", Type: Integer, BootDefault: "100", IntegerMin: 100, IntegerMax: 100, Description: "Shows the maximum number of function arguments."},
		{Name: "max_identifier_length", Type: Integer, BootDefault: "63", IntegerMin: 63, IntegerMax: 63, Description: "Shows the maximum identifier length."},
		{Name: "max_index_keys", Type: Integer, BootDefault: "32", IntegerMin: 32, IntegerMax: 32, Description: "Shows the maximum number of index keys."},
		{Name: "num_os_semaphores", Type: Integer, BootDefault: "0", IntegerMax: math.MaxInt32, Description: "Shows the number of semaphores required for the server."},
		{Name: "segment_size", Type: Integer, Unit: Blocks8kB, BootDefault: "131072", IntegerMin: 131072, IntegerMax: 131072, Description: "Shows the number of pages per disk file."},
		{Name: "server_encoding", Type: String, BootDefault: "SQL_ASCII", NativeStartup: &StartupDefault{Value: "UTF8", Source: "default"}, Description: "Shows the server (database) character set encoding."},
		{Name: "server_version", Type: String, BootDefault: "18.0", Description: "Shows the server version."},
		{Name: "server_version_num", Type: Integer, BootDefault: "180000", IntegerMin: 180000, IntegerMax: 180000, Description: "Shows the server version as an integer."},
		{Name: "shared_memory_size", Type: Integer, Unit: Megabytes, BootDefault: "0", IntegerMax: math.MaxInt32, Description: "Shows the size of the server's main shared memory area (rounded up to the nearest MB)."},
		{Name: "shared_memory_size_in_huge_pages", Type: Integer, BootDefault: "-1", IntegerMin: -1, IntegerMax: math.MaxInt32, Description: "Shows the number of huge pages needed for the main shared memory area.", ExtraDescription: "-1 means huge pages are not supported."},
		{Name: "ssl_library", Type: String, BootDefault: "", Description: "Shows the name of the SSL library."},
		{Name: "wal_block_size", Type: Integer, BootDefault: "8192", IntegerMin: 8192, IntegerMax: 8192, Description: "Shows the block size in the write ahead log."},
		{Name: "wal_segment_size", Type: Integer, Unit: Bytes, BootDefault: "16777216", IntegerMin: 1048576, IntegerMax: 1073741824, Description: "Shows the size of write ahead log segments."},
	}
	for i := range definitions {
		definitions[i].Access = ReadOnly
		definitions[i].Category = "Preset Options"
		definitions[i].FixedDefault = true
	}
	return definitions
}()
