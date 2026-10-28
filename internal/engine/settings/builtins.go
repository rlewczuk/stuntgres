package settings

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/pgencoding18"
)

var applicationName = Definition{
	Name: "application_name", Type: String,
	Description: "Sets the application name to be reported in statistics and logs.",
	Normalize:   normalizeApplicationName,
}
var builtins = []Definition{
	applicationName,
	{Name: "is_superuser", Type: Boolean, Access: ReadOnly, BootDefault: "off", Description: "Shows whether the current user is a superuser."},
	{Name: "deadlock_timeout", Type: Integer, Access: Privileged, Unit: Milliseconds, IntegerMin: 1, IntegerMax: math.MaxInt32, BootDefault: "1000", Description: "Sets the time to wait on a lock before checking for deadlock."},
	{Name: "max_locks_per_transaction", Type: Integer, Access: StartupOnly, IntegerMin: 10, IntegerMax: math.MaxInt32, BootDefault: "64", Description: "Sets the maximum number of locks per transaction."},
	{Name: "max_pred_locks_per_page", Type: Integer, Access: Sighup, IntegerMax: math.MaxInt32, BootDefault: "2", Description: "Sets the maximum number of predicate-locked tuples per page."},
	{Name: "max_pred_locks_per_relation", Type: Integer, Access: Sighup, IntegerMin: math.MinInt32, IntegerMax: math.MaxInt32, BootDefault: "-2", Description: "Sets the maximum number of predicate-locked pages and tuples per relation."},
	{Name: "max_pred_locks_per_transaction", Type: Integer, Access: StartupOnly, IntegerMin: 10, IntegerMax: math.MaxInt32, BootDefault: "64", Description: "Sets the maximum number of predicate locks per transaction."},
	{Name: "config_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/postgresql.conf"}, Description: "Sets the server's main configuration file."},
	{Name: "data_directory", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/data"}, Description: "Sets the server's data directory."},
	{Name: "external_pid_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{ResetNull: true}, Description: "Writes the postmaster PID to the specified file."},
	{Name: "hba_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/pg_hba.conf"}, Description: `Sets the server's "hba" configuration file.`},
	{Name: "ident_file", Type: String, Access: StartupOnly, BootDefaultNull: true, ReadRestricted: true, NativeStartup: &StartupDefault{Value: "/stuntgres/simulated/pg_ident.conf"}, Description: `Sets the server's "ident" configuration file.`},
	{Name: "authentication_timeout", Type: Integer, Access: Sighup, Unit: Seconds, IntegerMin: 1, IntegerMax: 600, BootDefault: "60", Description: "Sets the maximum allowed time to complete client authentication."},
	{Name: "bonjour", Type: Boolean, Access: StartupOnly, BootDefault: "off", Description: "Enables advertising the server via Bonjour."},
	{Name: "bonjour_name", Type: String, Access: StartupOnly, Description: "Sets the Bonjour service name."},
	{Name: "client_connection_check_interval", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the time interval between checks for disconnection while running queries."},
	{Name: "gss_accept_delegation", Type: Boolean, Access: Sighup, BootDefault: "off", Description: "Sets whether GSSAPI delegation should be accepted from the client."},
	{Name: "krb_caseins_users", Type: Boolean, Access: Sighup, BootDefault: "off", Description: "Sets whether Kerberos and GSSAPI user names should be treated as case-insensitive."},
	{Name: "krb_server_keyfile", Type: String, Access: Sighup, ReadRestricted: true, BootDefault: "FILE:/etc/krb5.keytab", Description: "Sets the location of the Kerberos server key file."},
	{Name: "listen_addresses", Type: String, Access: StartupOnly, BootDefault: "localhost", Description: "Sets the host name or IP address(es) to listen to."},
	{Name: "max_connections", Type: Integer, Access: StartupOnly, IntegerMin: 1, IntegerMax: 262143, BootDefault: "100", Description: "Sets the maximum number of concurrent connections."},
	{Name: "md5_password_warnings", Type: Boolean, BootDefault: "on", Description: "Enables deprecation warnings for MD5 passwords."},
	{Name: "oauth_validator_libraries", Type: String, Access: Sighup, ReadRestricted: true, Description: "Lists libraries that may be called to validate OAuth v2 bearer tokens."},
	{Name: "password_encryption", Type: Enum, BootDefault: "scram-sha-256", EnumValues: []string{"md5", "scram-sha-256"}, Description: "Chooses the algorithm for encrypting passwords."},
	{Name: "port", Type: Integer, Access: StartupOnly, IntegerMin: 1, IntegerMax: 65535, BootDefault: "5432", Description: "Sets the TCP port the server listens on."},
	{Name: "reserved_connections", Type: Integer, Access: StartupOnly, IntegerMax: 262143, BootDefault: "0", Description: "Sets the number of connection slots reserved for roles with privileges of pg_use_reserved_connections."},
	{Name: "scram_iterations", Type: Integer, IntegerMin: 1, IntegerMax: math.MaxInt32, BootDefault: "4096", Description: "Sets the iteration count for SCRAM secret generation."},
	{Name: "ssl", Type: Boolean, Access: Sighup, BootDefault: "off", Description: "Enables SSL connections."},
	{Name: "ssl_ca_file", Type: String, Access: Sighup, Description: "Location of the SSL certificate authority file."},
	{Name: "ssl_cert_file", Type: String, Access: Sighup, BootDefault: "server.crt", Description: "Location of the SSL server certificate file."},
	{Name: "ssl_ciphers", Type: String, Access: Sighup, ReadRestricted: true, BootDefault: "HIGH:MEDIUM:+3DES:!aNULL", Description: "Sets the list of allowed TLSv1.2 (and lower) ciphers."},
	{Name: "ssl_crl_dir", Type: String, Access: Sighup, Description: "Location of the SSL certificate revocation list directory."},
	{Name: "ssl_crl_file", Type: String, Access: Sighup, Description: "Location of the SSL certificate revocation list file."},
	{Name: "ssl_dh_params_file", Type: String, Access: Sighup, ReadRestricted: true, Description: "Location of the SSL DH parameters file."},
	{Name: "ssl_groups", Type: String, Access: Sighup, ReadRestricted: true, BootDefault: "X25519:prime256v1", Description: "Sets the group(s) to use for Diffie-Hellman key exchange."},
	{Name: "ssl_key_file", Type: String, Access: Sighup, BootDefault: "server.key", Description: "Location of the SSL server private key file."},
	{Name: "ssl_max_protocol_version", Type: Enum, Access: Sighup, ReadRestricted: true, EnumValues: []string{"", "TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"}, Description: "Sets the maximum SSL/TLS protocol version to use."},
	{Name: "ssl_min_protocol_version", Type: Enum, Access: Sighup, ReadRestricted: true, BootDefault: "TLSv1.2", EnumValues: []string{"TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"}, Description: "Sets the minimum SSL/TLS protocol version to use."},
	{Name: "ssl_passphrase_command", Type: String, Access: Sighup, ReadRestricted: true, Description: "Command to obtain passphrases for SSL."},
	{Name: "ssl_passphrase_command_supports_reload", Type: Boolean, Access: Sighup, BootDefault: "off", Description: `Controls whether "ssl_passphrase_command" is called during server reload.`},
	{Name: "ssl_prefer_server_ciphers", Type: Boolean, Access: Sighup, BootDefault: "on", Description: "Give priority to server ciphersuite order."},
	{Name: "ssl_tls13_ciphers", Type: String, Access: Sighup, ReadRestricted: true, Description: "Sets the list of allowed TLSv1.3 cipher suites."},
	{Name: "superuser_reserved_connections", Type: Integer, Access: StartupOnly, IntegerMax: 262143, BootDefault: "3", Description: "Sets the number of connection slots reserved for superusers."},
	{Name: "tcp_keepalives_count", Type: Integer, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Maximum number of TCP keepalive retransmits."},
	{Name: "tcp_keepalives_idle", Type: Integer, Unit: Seconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Time between issuing TCP keepalives."},
	{Name: "tcp_keepalives_interval", Type: Integer, Unit: Seconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Time between TCP keepalive retransmits."},
	{Name: "tcp_user_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "TCP user timeout."},
	{Name: "unix_socket_directories", Type: String, Access: StartupOnly, ReadRestricted: true, BootDefault: "/tmp", Description: "Sets the directories where Unix-domain sockets will be created."},
	{Name: "unix_socket_group", Type: String, Access: StartupOnly, Description: "Sets the owning group of the Unix-domain socket."},
	{Name: "unix_socket_permissions", Type: Integer, Access: StartupOnly, IntegerMax: 511, BootDefault: "511", Description: "Sets the access permissions of the Unix-domain socket."},
	{Name: "row_security", Type: Boolean, BootDefault: "on", Description: "Enables row security."},
	{Name: "plpgsql.check_asserts", Type: Boolean, BootDefault: "on", Description: "Perform checks given in ASSERT statements."},
	{Name: "track_commit_timestamp", Type: Boolean, Access: StartupOnly, BootDefault: "off", Description: "Collects transaction commit time."},
	{Name: "default_transaction_isolation", Type: Enum, BootDefault: "read committed", EnumValues: []string{"serializable", "repeatable read", "read committed", "read uncommitted"}, Description: "Sets the transaction isolation level of each new transaction."},
	{Name: "transaction_isolation", Type: Enum, BootDefault: "read committed", EnumValues: []string{"serializable", "repeatable read", "read committed", "read uncommitted"}, Description: "Sets the current transaction's isolation level."},
	{Name: "default_transaction_read_only", Type: Boolean, BootDefault: "off", Description: "Sets the default read-only status of new transactions."},
	{Name: "transaction_read_only", Type: Boolean, BootDefault: "off", Description: "Sets the current transaction's read-only status."},
	{Name: "default_transaction_deferrable", Type: Boolean, BootDefault: "off", Description: "Sets the default deferrable status of new transactions."},
	{Name: "transaction_deferrable", Type: Boolean, BootDefault: "off", Description: "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
	{Name: "bytea_output", Type: Enum, BootDefault: "hex", EnumValues: []string{"escape", "hex"}, Description: "Sets the output format for bytea."},
	{Name: "timezone", Type: String, BootDefault: "GMT", NativeStartup: &StartupDefault{Value: "UTC"}, Description: "Sets the time zone for displaying and interpreting time stamps."},
	{Name: "datestyle", Type: String, BootDefault: "ISO, MDY", Description: "Sets the display format for date and time values."},
	{Name: "intervalstyle", Type: Enum, BootDefault: "postgres", EnumValues: []string{"postgres", "postgres_verbose", "sql_standard", "iso_8601"}, Description: "Sets the display format for interval values."},
	{Name: "timezone_abbreviations", Type: String, BootDefaultNull: true, NativeStartup: &StartupDefault{Value: "Default"}, Description: "Selects a file of time zone abbreviations."},
	ClientMinMessages,
	{Name: "client_encoding", Type: String, BootDefault: "SQL_ASCII", NativeStartup: &StartupDefault{Value: "UTF8"}, Description: "Sets the client's character set encoding.", Normalize: normalizeClientEncoding},
	{Name: "lc_messages", Type: String, Access: Privileged, NativeStartup: &StartupDefault{Value: "C"}, Description: "Sets the language in which messages are displayed.", Normalize: normalizeLCMessages},
	{Name: "lc_monetary", Type: String, BootDefault: "C", Description: "Sets the locale for formatting monetary amounts.", Normalize: normalizeLCMonetary},
	{Name: "lc_numeric", Type: String, BootDefault: "C", Description: "Sets the locale for formatting numbers.", Normalize: normalizeLCNumeric},
	{Name: "lc_time", Type: String, BootDefault: "C", Description: "Sets the locale for formatting date and time values.", Normalize: normalizeLCTime},
	icuValidationLevel,
	{Name: "extra_float_digits", Type: Integer, BootDefault: "1", IntegerMin: -15, IntegerMax: 3, Description: "Sets the number of digits displayed for floating-point values."},
	{Name: "search_path", Type: String, BootDefault: `"$user", public`, Description: "Sets the schema search order for names that are not schema-qualified."},
	{Name: "log_statement_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Description: "Writes cumulative performance statistics to the server log."},
	{Name: "allow_alter_system", Type: Boolean, Access: Sighup, BootDefault: "on", Description: "Allows running the ALTER SYSTEM command."},
	{Name: "array_nulls", Type: Boolean, BootDefault: "on", Description: "Enables input of NULL elements in arrays."},
	{Name: "backslash_quote", Type: Enum, BootDefault: "safe_encoding", EnumValues: []string{"safe_encoding", "on", "off"}, Normalize: normalizeBackslashQuote, Description: `Sets whether "\'" is allowed in string literals.`},
	{Name: "escape_string_warning", Type: Boolean, BootDefault: "on", Description: "Warn about backslash escapes in ordinary string literals."},
	{Name: "lo_compat_privileges", Type: Boolean, Access: Privileged, BootDefault: "off", Description: "Enables backward compatibility mode for privilege checks on large objects."},
	{Name: "synchronize_seqscans", Type: Boolean, BootDefault: "on", Description: "Enables synchronized sequential scans."},
	{Name: "transform_null_equals", Type: Boolean, BootDefault: "off", Description: `Treats "expr=NULL" as "expr IS NULL".`},
	{Name: "standard_conforming_strings", Type: Boolean, BootDefault: "on", Description: "Causes '...' strings to treat backslashes literally."},
	{Name: "quote_all_identifiers", Type: Boolean, BootDefault: "off", Description: "When generating SQL fragments, quote all identifiers."},
	{Name: "statement_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any statement."},
	{Name: "lock_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any wait for a lock."},
	{Name: "max_parallel_workers_per_gather", Type: Integer, IntegerMax: 1024, BootDefault: "2", Description: "Sets the maximum number of parallel processes per executor node."},
	{Name: "transaction_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
	{Name: "idle_in_transaction_session_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed idle time between queries, when in a transaction."},
	{Name: "idle_session_timeout", Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed idle time between queries, when not in a transaction."},
	{Name: "check_function_bodies", Type: Boolean, BootDefault: "on", Description: "Check routine bodies during CREATE FUNCTION and CREATE PROCEDURE."},
	{Name: "createrole_self_grant", Type: String, Normalize: normalizeCreateroleSelfGrant, Description: "Sets whether a CREATEROLE user automatically grants the role to themselves, and with which options."},
	{Name: "default_table_access_method", Type: String, BootDefault: "heap", Normalize: NormalizeCatalogName, Description: "Sets the default table access method for new tables."},
	{Name: "default_tablespace", Type: String, Normalize: NormalizeCatalogName, Description: "Sets the default tablespace to create tables and indexes in."},
	{Name: "default_text_search_config", Type: String, BootDefault: "pg_catalog.simple", Description: "Sets default text search configuration."},
	{Name: "default_toast_compression", Type: Enum, BootDefault: "pglz", EnumValues: []string{"pglz", "lz4"}, Description: "Sets the default compression method for compressible values."},
	{Name: "dynamic_library_path", Type: String, Access: Privileged, ReadRestricted: true, BootDefault: "$libdir", Description: "Sets the path for dynamically loadable modules."},
	{Name: "event_triggers", Type: Boolean, Access: Privileged, BootDefault: "on", Description: "Enables event triggers."},
	{Name: "extension_control_path", Type: String, Access: Privileged, ReadRestricted: true, BootDefault: "$system", Description: "Sets the path for extension control files."},
	{Name: "gin_fuzzy_search_limit", Type: Integer, IntegerMax: math.MaxInt32, BootDefault: "0", Description: "Sets the maximum allowed result for exact search by GIN."},
	{Name: "gin_pending_list_limit", Type: Integer, Unit: Kilobytes, IntegerMin: 64, IntegerMax: math.MaxInt32, BootDefault: "4096", Description: "Sets the maximum size of the pending list for GIN index."},
	{Name: "jit_provider", Type: String, Access: StartupOnly, ReadRestricted: true, BootDefault: "llvmjit", Description: "JIT provider to use."},
	{Name: "local_preload_libraries", Type: String, Description: "Lists unprivileged shared libraries to preload into each backend."},
	{Name: "restrict_nonsystem_relation_kind", Type: String, Normalize: normalizeRestrictedRelationKind, Description: "Prohibits access to non-system relations of specified kinds."},
	{Name: "session_preload_libraries", Type: String, Access: Privileged, ReadRestricted: true, Description: "Lists shared libraries to preload into each backend."},
	{Name: "session_replication_role", Type: Enum, Access: Privileged, BootDefault: "origin", EnumValues: []string{"origin", "replica", "local"}, Description: "Sets the session's behavior for triggers and rewrite rules."},
	{Name: "shared_preload_libraries", Type: String, Access: StartupOnly, ReadRestricted: true, Description: "Lists shared libraries to preload into server."},
	{Name: "temp_tablespaces", Type: String, Description: "Sets the tablespace(s) to use for temporary tables and sort files."},
	{Name: "xmlbinary", Type: Enum, BootDefault: "base64", EnumValues: []string{"base64", "hex"}, Description: "Sets how binary values are to be encoded in XML."},
	{Name: "xmloption", Type: Enum, BootDefault: "content", EnumValues: []string{"content", "document"}, Description: "Sets whether XML data in implicit parsing and serialization operations is to be considered as documents or content fragments."},
}

func normalizeApplicationName(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	return applicationNameValue(value)
}

// StartupApplicationName admits the original pre-converter bytes. The database
// is UTF8 already: NAME clipping uses its mblen rule, not validation or decoding
// with the requested client encoding. The returned value must not be reparsed.
func StartupApplicationName(value string) (Value, *sqlstate.Error, error) {
	if strings.IndexByte(value, 0) >= 0 {
		return Value{}, nil, errors.New("stuntgres: NUL startup application name")
	}
	text, notice, err := applicationNameValue(value)
	return Value{Text: text}, notice, err
}

func applicationNameValue(value string) (string, *sqlstate.Error, error) {
	var notice *sqlstate.Error
	if len(value) > 63 {
		n := 0
		for n < len(value) {
			width := pgencoding18.Length(6, value[n:])
			if n+width > 63 {
				break
			}
			n += width
		}
		notice = &sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, value, value[:n])}
		value = value[:n]
	}
	return sqltype.CleanApplicationName(value), notice, nil
}
