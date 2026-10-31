package settings

// builtinMetadata completes the source-owned builtin descriptors. Registered
// definitions receive their separate native registration category in Validate.
func builtinMetadata(d Definition) Definition {
	d.Category = "Client Connection Defaults / Statement Behavior"
	switch d.Name {
	case "stats_fetch_consistency", "track_activities", "track_counts", "track_functions", "track_io_timing", "track_wal_io_timing":
		d.Category = "Statistics / Cumulative Query and Index Statistics"
	case "is_superuser":
		d.Category = "Preset Options"
	case "deadlock_timeout", "max_locks_per_transaction", "max_pred_locks_per_page", "max_pred_locks_per_relation", "max_pred_locks_per_transaction":
		d.Category = "Lock Management"
	case "config_file", "data_directory", "external_pid_file", "hba_file", "ident_file":
		d.Category = "File Locations"
	case "bonjour", "bonjour_name", "listen_addresses", "max_connections", "port", "reserved_connections", "superuser_reserved_connections", "unix_socket_directories", "unix_socket_group", "unix_socket_permissions":
		d.Category = "Connections and Authentication / Connection Settings"
	case "authentication_timeout", "gss_accept_delegation", "krb_caseins_users", "krb_server_keyfile", "md5_password_warnings", "oauth_validator_libraries", "password_encryption", "scram_iterations":
		d.Category = "Connections and Authentication / Authentication"
	case "client_connection_check_interval", "tcp_keepalives_count", "tcp_keepalives_idle", "tcp_keepalives_interval", "tcp_user_timeout":
		d.Category = "Connections and Authentication / TCP Settings"
	case "ssl", "ssl_ca_file", "ssl_cert_file", "ssl_ciphers", "ssl_crl_dir", "ssl_crl_file", "ssl_dh_params_file", "ssl_groups", "ssl_key_file", "ssl_max_protocol_version", "ssl_min_protocol_version", "ssl_passphrase_command", "ssl_passphrase_command_supports_reload", "ssl_prefer_server_ciphers", "ssl_tls13_ciphers":
		d.Category = "Connections and Authentication / SSL"
	case "plpgsql.check_asserts":
		d.Category = "Customized Options"
	case "application_name":
		d.Category = "Reporting and Logging / What to Log"
	case "track_commit_timestamp":
		d.Category = "Replication / Sending Servers"
	case "timezone", "datestyle", "intervalstyle", "timezone_abbreviations", "client_encoding", "lc_messages", "lc_monetary", "lc_numeric", "lc_time", "icu_validation_level", "extra_float_digits", "default_text_search_config":
		d.Category = "Client Connection Defaults / Locale and Formatting"
	case "local_preload_libraries", "session_preload_libraries", "shared_preload_libraries", "jit_provider":
		d.Category = "Client Connection Defaults / Shared Library Preloading"
	case "dynamic_library_path", "extension_control_path", "gin_fuzzy_search_limit":
		d.Category = "Client Connection Defaults / Other Defaults"
	case "log_statement_stats":
		d.Category = "Statistics / Monitoring"
	case "array_nulls", "backslash_quote", "escape_string_warning", "lo_compat_privileges", "standard_conforming_strings", "quote_all_identifiers", "synchronize_seqscans":
		d.Category = "Version and Platform Compatibility / Previous PostgreSQL Versions"
	case "allow_alter_system", "transform_null_equals":
		d.Category = "Version and Platform Compatibility / Other Platforms and Clients"
	case "max_parallel_workers_per_gather":
		d.Category = "Resource Usage / Worker Processes"
	}
	switch d.Name {
	case "max_locks_per_transaction":
		d.ExtraDescription = `The shared lock table is sized on the assumption that at most "max_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`
	case "max_pred_locks_per_page":
		d.ExtraDescription = "If more than this number of tuples on the same page are locked by a connection, those locks are replaced by a page-level lock."
	case "max_pred_locks_per_relation":
		d.ExtraDescription = "If more than this total of pages and tuples in the same relation are locked by a connection, those locks are replaced by a relation-level lock."
	case "max_pred_locks_per_transaction":
		d.ExtraDescription = `The shared predicate lock table is sized on the assumption that at most "max_pred_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`
	case "bonjour_name":
		d.ExtraDescription = "An empty string means use the computer name."
	case "unix_socket_group":
		d.ExtraDescription = "The owning user of the socket is always the user that starts the server. An empty string means use the user's default group."
	case "unix_socket_permissions":
		d.ExtraDescription = "Unix-domain sockets use the usual Unix file system permission set. The parameter value is expected to be a numeric mode specification in the form accepted by the chmod and umask system calls. (To use the customary octal format the number must start with a 0 (zero).)"
	case "client_connection_check_interval":
		d.ExtraDescription = "0 disables connection checks."
	case "tcp_keepalives_idle", "tcp_keepalives_interval", "tcp_user_timeout":
		d.ExtraDescription = "0 means use the system default."
	case "tcp_keepalives_count":
		d.ExtraDescription = "Number of consecutive keepalive retransmits that can be lost before a connection is considered dead. 0 means use the system default."
	case "ssl_tls13_ciphers":
		d.ExtraDescription = "An empty string means use the default cipher suites."
	case "ssl_groups":
		d.ExtraDescription = "Multiple groups can be specified using a colon-separated list."
	case "ssl_dh_params_file":
		d.ExtraDescription = "An empty string means use compiled-in default parameters."
	case "ssl_passphrase_command":
		d.ExtraDescription = "An empty string means use the built-in prompting mechanism."
	case "allow_alter_system":
		d.ExtraDescription = "Can be set to off for environments where global configuration changes should be made using a different method."
	case "array_nulls":
		d.ExtraDescription = "When turned on, unquoted NULL in an array input value means a null value; otherwise it is taken literally."
	case "lo_compat_privileges":
		d.ExtraDescription = "Skips privilege checks when reading or modifying large objects, for compatibility with PostgreSQL releases prior to 9.0."
	case "transform_null_equals":
		d.ExtraDescription = "When turned on, expressions of the form expr = NULL (or NULL = expr) are treated as expr IS NULL, that is, they return true if expr evaluates to the null value, and false otherwise. The correct behavior of expr = NULL is to always return null (unknown)."
	case "event_triggers":
		d.ExtraDescription = "When enabled, event triggers will fire for all applicable statements."
	case "gin_fuzzy_search_limit":
		d.ExtraDescription = "0 means no limit."
	case "default_tablespace", "temp_tablespaces":
		d.ExtraDescription = "An empty string means use the database's default tablespace."
	case "createrole_self_grant":
		d.ExtraDescription = "An empty string disables automatic self grants."
	case "dynamic_library_path":
		d.ExtraDescription = "If a dynamically loadable module needs to be opened and the specified name does not have a directory component (i.e., the name does not contain a slash), the system will search this path for the specified file."
	case "extension_control_path":
		d.ExtraDescription = "The remaining extension script and secondary control files are then loaded from the same directory where the primary control file was found."
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
