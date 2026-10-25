package settings

// builtinMetadata completes the source-owned builtin descriptors. Registered
// definitions receive their separate native registration category in Validate.
func builtinMetadata(d Definition) Definition {
	d.Category = "Client Connection Defaults / Statement Behavior"
	switch d.Name {
	case "config_file", "data_directory", "external_pid_file", "hba_file", "ident_file":
		d.Category = "File Locations"
	case "plpgsql.check_asserts":
		d.Category = "Customized Options"
	case "application_name":
		d.Category = "Reporting and Logging / What to Log"
	case "track_commit_timestamp":
		d.Category = "Replication / Sending Servers"
	case "timezone", "datestyle", "intervalstyle", "timezone_abbreviations", "client_encoding", "lc_messages", "lc_monetary", "lc_numeric", "lc_time", "icu_validation_level", "extra_float_digits":
		d.Category = "Client Connection Defaults / Locale and Formatting"
	case "log_statement_stats":
		d.Category = "Statistics / Monitoring"
	case "server_encoding":
		d.Category = "Preset Options"
	case "standard_conforming_strings", "quote_all_identifiers":
		d.Category = "Version and Platform Compatibility / Previous PostgreSQL Versions"
	case "max_parallel_workers_per_gather":
		d.Category = "Resource Usage / Worker Processes"
	case "enable_async_append":
		d.Category = "Query Tuning / Planner Method Configuration"
	}
	switch d.Name {
	case "row_security":
		d.ExtraDescription = "When enabled, row security will be applied to all users."
	case "statement_timeout", "lock_timeout", "transaction_timeout", "idle_in_transaction_session_timeout", "idle_session_timeout":
		d.ExtraDescription = "0 disables the timeout."
	case "datestyle":
		d.ExtraDescription = "Also controls interpretation of ambiguous date inputs."
	case "lc_messages", "lc_monetary", "lc_numeric", "lc_time":
		d.ExtraDescription = "An empty string means use the operating system setting."
	case "client_min_messages":
		d.ExtraDescription = "Each level includes all the levels that follow it. The later the level, the fewer messages are sent."
	case "extra_float_digits":
		d.ExtraDescription = "This affects real, double precision, and geometric data types. A zero or negative parameter value is added to the standard number of digits (FLT_DIG or DBL_DIG as appropriate). Any value greater than zero selects precise output mode."
	}
	return d
}
