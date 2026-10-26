# internal/engine/settings

Setting definitions and their pure behavior: the definition types, the
builtin table, registration validation, typed parsing and formatting, unit
conversion, the normalizers for encoding, locale and message-level settings,
the `search_path` list parser and the immutable registry. The package is a
leaf of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports only `sqlstate`, `sqltype` and the standard library, and never
the engine. The root driver converts its public `SettingDefinition` mirror
to `Definition` and `cmd/stuntgres` implements `SessionDefaultProvider`; both
import this package directly.

## What it owns

- **Definitions** (`definition.go`, `metadata.go`): `Definition` with `Type`,
  `Access`, `Unit`, category and optional extra description; builtin metadata
  follows PostgreSQL 18 English wording. `Validate` captures the custom
  category `Customized Options`, dotted names, bounds, enums and exact typed
  boot values; `BootValue`, `DefaultValue`, `FoldName`, `CustomName`, `DisplayName` and
  `SessionDefaultProvider`. Validation errors use the same `"stuntgres: "`
  prefix as the engine's `RegistrationError`, which stays in the engine
  because foreign-data registration shares it.
- **Builtins** (`builtins.go`, `developer.go`, `logging.go`, `messages.go`, `format_encoding.go`): the
  private builtin table in PostgreSQL 18 English `short_desc` wording, the
  `application_name`, `client_min_messages` (`ClientMinMessages`),
  encoding, locale and `icu_validation_level` normalizers. `MessageLevelRank`
  orders the client message levels; the `session.NoticeLevel` constants
  follow that order.
  `plpgsql.check_asserts` is an ordinary user-settable Boolean, booting on, with
  `Customized Options` metadata. Procedural execution reads its effective session
  value; registry construction does not capture an assertion decision.
- **Values** (`value.go`, `number.go`, `units.go`): `Value` and
  `Definition.Parse`/`Format`, base-unit `Raw` and current-catalog `CurrentRaw`,
  PostgreSQL-compatible number scanning (`ParseNumber`, also used for storage
  options) and memory/time units.
- **Search path** (`search_path.go`): the pure list parser. Namespace and ACL
  resolution are in `analyze` (`search_path.go`).
- **Registry** (`registry.go`): `NewRegistry(registered,
  trackCommitTimestamps)` captures builtin boot and effective native startup
  values once (the engine passes its commit-timestamp tracking flag), adds
  validated registered definitions and sorts the names. `Lookup`, `Definitions`
  and `Order` read it.

## Invariants

- **Immutable registry.** A `Registry` is built once and never changed. The
  engine holds one by value (`Engine.Settings`) and parameter providers
  borrow a pointer to it. `Definitions`, `Order`, enum values and boot
  `Value.Path` slices are borrowed without copies; callers must not mutate
  them. Reads need no lock.
- **No callbacks or state.** Nothing here calls default providers, reads a
  session, takes a lock or emits notices. Normalizers return a notice for
  the caller to emit.
- **Stable output.** Builtin order, boot values and descriptions, validation
  messages, SQLSTATEs, hints and unit conversions are compatibility
  contracts; `SHOW ALL` and the reference suite observe them.

## Preset options

`preset.go` owns all 20 `Preset Options` descriptors, each with internal context,
unrestricted reads and a fixed native effective default:

| Name | Native SHOW/current_setting | Raw/boot distinction |
|---|---|---|
| block_size | 8192 | fixed8192 |
| data_checksums | off | no disk checksum engine |
| data_directory_mode | 0700 | current octal; boot/reset448, bounds0..511 |
| debug_assertions | off | simulated C build flag, not Go assertions |
| huge_pages_status | off | boot unknown; enum off,on,unknown |
| in_hot_standby | off | no physical recovery |
| integer_datetimes | on | logical integer temporal representation |
| max_function_args | 100 | actual logical limit |
| max_identifier_length | 63 | actual UTF8-safe byte limit |
| max_index_keys | 32 | actual logical index-width limit |
| num_os_semaphores | 0 | no PostgreSQL IPC semaphores |
| segment_size | 1GB | raw/boot/reset131072 blocks, unit8kB |
| server_encoding | UTF8 | actual text invariant; boot SQL_ASCII |
| server_version | 18.0 | truthful embedded compatibility identity |
| server_version_num | 180000 | same fixed bounds and boot/reset |
| shared_memory_size | 0 | MB base unit; no PostgreSQL shared-memory area |
| shared_memory_size_in_huge_pages | -1 | unsupported area; bounds-1..2147483647 |
| ssl_library | empty string | boot/reset empty, not NULL; no PostgreSQL OpenSSL ABI |
| wal_block_size | 8192 | simulated WAL block |
| wal_segment_size | 16MB | raw/boot/reset16777216 bytes, bounds1MB..1GB |

These physical values are explicitly authorized **setting-only simulations**,
not the technical-statistics approximation exception. They do not create files,
chmod directories, checksum pages, generate WAL, map shared memory, allocate huge
pages/semaphores, load an SSL library, enter recovery or perform a restart.
Go TLS support is unrelated to the empty PostgreSQL SSL-library identity.
`num_os_semaphores` and `shared_memory_size` have int32 nonnegative bounds.
Other Boolean/string metadata and source fields follow the release descriptors;
native initialized defaults have source `default`, not `override`.

All roles, including ordinary logins, read every preset. Individual SET/RESET,
DEFAULT and set_config are refused before parsing/authorization with 55P02;
parameter grants cannot unlock them. RESET ALL skips presets. Builtin-only
`FixedDefault` rejects differing parsed provider values while accepting normalized
equivalents and preserving custom ReadOnly providers. `Blocks8kB` is internal:
public custom-unit registration remains unchanged. Physical sessions retain
these values across pool reuse and logical native controls. The pinned comparison
server is PostgreSQL18.6, independently of native's truthful18.0 identity.

## Lock settings

`deadlock_timeout`, `max_locks_per_transaction`, `max_pred_locks_per_page`,
`max_pred_locks_per_relation` and `max_pred_locks_per_transaction` are typed,
unrestricted-read `Lock Management` settings. These are setting-only simulations:
they do not schedule deadlock checks, size lock tables, enforce lock budgets,
track predicate locks or promote predicate locks. Existing native deadlock
detection and logical constraints remain active.

`deadlock_timeout` uses millisecond parsing and effective superuser/parameter
SET authority. The transaction limits have postmaster context; page and relation
thresholds have sighup context. Every individual live mutation of those four
immutable values is rejected before parsing, even with a SET grant. Providers
may supply only normalized equivalent immutable defaults. Session overrides,
LOCAL, savepoints, resets and physical-session capture reuse the ordinary store;
RESET ALL skips immutable values and native controls do not serialize settings.

## Developer settings

`developer.go` registers the 24 Developer Options available in the pinned
PostgreSQL release build. The eleven compile-conditional debug names absent
from that build remain unknown parameters, including for privileged readers.
All available rows have unrestricted reads and exact typed boot/reset metadata;
`debug_discard_caches` has the release-build domain 0..0.

These are setting-only simulations. They do not enable system-table bypass,
physical tablespaces, WAL checking/recovery, direct I/O, cache eviction, JIT,
parallel workers, debugger hooks, tracing, signals or authentication delays.
Logical constraints, ACLs and query behavior remain active.

Backtrace strings use the ASCII character whitelist without identifier lookup.
WAL lists validate mask-capable builtin resource managers with identifier-list
syntax; direct-I/O lists use non-folding/non-truncating GUC-list syntax.
All preserve accepted input spelling. Only WAL and direct-I/O settings flatten
multiple SET arguments; backtrace takes exactly one argument.

Builtin-only Backend and SuperuserBackend access project `backend` and
`superuser-backend`; both reject live mutation before parsing or authority.
Trusted session-default capture may assign these once, without implementing
client startup authentication. StartupOnly and Sighup still accept only their
normalized initial values from providers. Developer superuser settings use
effective parameter SET authority; grants never unlock immutable contexts.

## Logging settings

`logging.go` owns 41 logging definitions; `application_name` retains its existing
definition and clipping/notice consumer. Together the family comprises:
`application_name`, `cluster_name`, `debug_pretty_print`, `debug_print_parse`,
`event_source`, `log_autovacuum_min_duration`, `log_checkpoints`,
`log_connections`, `log_destination`, `log_directory`, `log_disconnections`,
`log_duration`, `log_error_verbosity`, `log_file_mode`, `log_filename`,
`log_hostname`, `log_line_prefix`, `log_lock_failures`, `log_lock_waits`,
`log_min_duration_sample`, `log_min_duration_statement`,
`log_min_error_statement`, `log_min_messages`, `log_parameter_max_length`,
`log_parameter_max_length_on_error`, `log_recovery_conflict_waits`,
`log_replication_commands`, `log_rotation_age`, `log_rotation_size`,
`log_startup_progress_interval`, `log_statement`, `log_statement_sample_rate`,
`log_temp_files`, `log_timezone`, `log_transaction_sample_rate`,
`log_truncate_on_rotation`, `logging_collector`, `syslog_facility`,
`syslog_ident`, `syslog_sequence_numbers`, `syslog_split_messages` and
`update_process_title`.

These are validated, typed **setting-only simulations**, not file, collector,
syslog/eventlog, restart, process-title, DNS, lock-reporting, sampling, checkpoint,
autovacuum, recovery, replication or temporary-file mechanisms. They never
change logical data/constraints, client diagnostics, client message filtering or
session time-zone rendering. Defaults model the released Linux-style build,
not host capabilities. `log_timezone` boots and remains GMT with source default;
there is no invented initdb or configuration-file assignment. Native provenance
stays NULL and pending_restart stays false. An initialized reference server can
have genuine configured timezone and command-line prefix defaults.

The three ordinary user settings besides application_name are debug_pretty_print,
debug_print_parse and log_parameter_max_length_on_error. Mutable superuser
logging settings use effective parameter SET authority. Startup, sighup and
superuser-backend contexts reject every live mutation before parsing/ACL checks;
only the two backend defaults may differ at trusted physical-session capture.
RESET ALL skips immutable contexts and restores eligible mutable overrides even
after SET revocation. Providers capture pure normalized values once and never
perform physical effects. Only log_directory/log_filename restrict reads;
SET grants never disclose them, and aggregate views omit unauthorized rows.

Only log_connections/log_destination flatten multiple SET arguments. Their
identifier-list validators preserve accepted spelling; log_connections supports
the PG18 exact standalone Boolean compatibility aliases, not Boolean prefixes.
Directory cleanup is lexical POSIX cleanup with no filesystem I/O. Cluster names
reuse application-name UTF8 clipping before ASCII escaping. Server severity enums
are separate from client filtering and accept hidden debug as debug2.
log_timezone resolves only named/POSIX zones and uses the release epoch-2000
seconds probe on the supported leap-free zone domain, not session numeric/SQL
INTERVAL syntax. File mode current output is four-digit octal; boot/reset/bounds
remain decimal. Numeric units/ranges reuse the shared parser.

## Simulated file-location defaults

The five restricted-read string definitions have `StartupOnly` access, category
`File Locations` and SQL context `postmaster`. Their effective native initial
values are immutable simulated labels:

| Setting | Initial/current value | Reset metadata |
| --- | --- | --- |
| `config_file` | `/stuntgres/simulated/postgresql.conf` | same string |
| `data_directory` | `/stuntgres/simulated/data` | same string |
| `external_pid_file` | empty string | SQL NULL |
| `hba_file` | `/stuntgres/simulated/pg_hba.conf` | same string |
| `ident_file` | `/stuntgres/simulated/pg_ident.conf` | same string |

All five boot metadata values are SQL NULL. `BootDefaultNull` describes that
metadata; `NativeStartup` separately owns the immutable `StartupDefault` and
cached typed effective default returned by `DefaultValue`. Current values remain
non-NULL strings. Ordinary custom empty defaults keep non-NULL empty boot/reset
metadata. Registration rejects these builtin-only flags and payloads.

The four explicit simulated assignments report source `override`; the unset PID
setting reports `default`. A same-value session provider retains source `client`,
but a different startup value is rejected. Neither the registry nor its consumers
load configuration/authentication files, select disk storage, create PID files,
or restart processes. The labels need not exist on the host.

## Client connection defaults

The PostgreSQL 18 client family shares this registry and the existing typed
SET/SHOW/current_setting/set_config/pg_settings path. Bytea, encoding, message
filtering, date/interval/time-zone presentation, C/POSIX locale environments,
float formatting, search_path, row security, transaction characteristics and
timeouts retain their real native consumers. Encoding remains UTF8-only;
recognized unsupported encodings report the existing capability error.

The following are **setting-only simulations**, not operational switches:
`check_function_bodies`, `createrole_self_grant`, `default_table_access_method`,
`default_tablespace`, `default_text_search_config`, `default_toast_compression`,
`dynamic_library_path`, `event_triggers`, `extension_control_path`,
`gin_fuzzy_search_limit`, `gin_pending_list_limit`, `local_preload_libraries`,
`restrict_nonsystem_relation_kind`, `session_preload_libraries`,
`session_replication_role`, `temp_tablespaces`, `xmlbinary` and `xmloption`.
Table AM/tablespace names use actual catalog identities and temporary
tablespaces check CREATE privileges. Text-search configuration names use a
finite released builtin-name resolver without creating pg_ts_config rows.
No compressors, GIN truncation/pending lists, physical placement, automatic
role membership, relation restrictions, FTS/XML execution or event-trigger
mechanisms are implied. In particular, native FK checks still run with
replication role replica, and invalid routines cannot publish with
check_function_bodies off. `SET XML OPTION DOCUMENT/CONTENT` (including
SESSION/LOCAL) is syntax for the same xmloption value.

Library/path settings never probe or load files. Preload SET argument lists
are quote-reconstructed, but raw malformed set_config strings are accepted
without eager loader/list checks. `jit_provider` and `shared_preload_libraries`
are restricted-read postmaster settings: all live changes fail with 55P02,
not a simulated restart or load.

Privileged selected builtins honor superuser or effective parameter SET ACLs;
restricted reads independently require pg_read_all_settings. SET privilege
alone never grants read access. Registered Privileged definitions and
log_statement_stats remain deny-only.

AM/default tablespace name settings apply PostgreSQL GUC_IS_NAME truncation at
63 UTF8-safe bytes, without folding case. Its NOTICE42622 precedes catalog
validation, including on failed assignments, and uses the prior message filter.
Catalog validation sees and diagnoses the clipped name; failure still preserves
the old setting and logical catalog.

Boot metadata differs from effective native startup/reset values:
client_encoding SQL_ASCII → UTF8, lc_messages empty → C, TimeZone GMT → UTC,
timezone_abbreviations SQL NULL → Default. Native FTS starts at
pg_catalog.simple; the pinned initialized oracle starts at pg_catalog.english.
Native startup assignments have truthful override sources and no source file
or line. Real initdb/pq configuration-file/client sources are separate oracle
facts, not native file provenance.

SET LOCAL, rollback/savepoints and RESET reuse the session action stack.
Returning a connection to a pool retains its physical settings; replacement
captures fresh defaults. Native Reset/Dump/Restore never serialize or reset
live settings.
RESET ALL restores previously SET eligible privileged builtins even after their
SET grant is revoked, as PostgreSQL does; individual RESET still checks current
authority. This does not unlock deny-only custom definitions or log_statement_stats.

The internal trusted native SessionDefaultProvider retains its pure typed
capture contract, not a PostgreSQL startup/catalog hook. AM/tablespace/FTS
provider strings may name nonexistent simulated objects; this is not evidence
those objects exist. Explicit SQL assigning an unknown name still fails.
DEFAULT/RESET restores the captured value without provider reentry or catalog
lookup. Providers still use pure parsing/normalization (including name clipping)
with silent initialization; replacement physical sessions capture defaults anew.


## Connection and authentication settings

The connection family uses this same registry, typed session store, context
checks and effective-user read authority. All 38 values are setting-only
simulations, not physical configuration consumers.

- Startup-only: `bonjour`, `bonjour_name`, `listen_addresses`, `max_connections`,
  `port`, `reserved_connections`, `superuser_reserved_connections`,
  `unix_socket_directories`, `unix_socket_group`, `unix_socket_permissions`.
- Sighup: `authentication_timeout`, `gss_accept_delegation`, `krb_caseins_users`,
  `krb_server_keyfile`, `oauth_validator_libraries`, `ssl`, `ssl_ca_file`,
  `ssl_cert_file`, `ssl_ciphers`, `ssl_crl_dir`, `ssl_crl_file`,
  `ssl_dh_params_file`, `ssl_groups`, `ssl_key_file`, `ssl_max_protocol_version`,
  `ssl_min_protocol_version`, `ssl_passphrase_command`,
  `ssl_passphrase_command_supports_reload`, `ssl_prefer_server_ciphers`,
  `ssl_tls13_ciphers`.
- Ordinary user-settable: `client_connection_check_interval`,
  `md5_password_warnings`, `password_encryption`, `scram_iterations`,
  `tcp_keepalives_count`, `tcp_keepalives_idle`, `tcp_keepalives_interval`,
  `tcp_user_timeout`.

Every individual startup/sighup mutation fails 55P02 before value parsing,
including same values, DEFAULT, RESET and set_config. RESET ALL skips them.
The three LIST_INPUT names `listen_addresses`, `unix_socket_directories` and
`oauth_validator_libraries` reach that same context refusal for multi-value SET;
other scalar settings retain the one-argument error.

The simulated Linux/OpenSSL-style build labels are `/tmp` for socket
directories and `FILE:/etc/krb5.keytab` for the Kerberos keyfile; SSL descriptor
defaults use the SSL-enabled branch. These labels need not exist and do not
describe host build capabilities. All connection boot/reset values are non-NULL,
including empty strings. Initial source is `default`, with no source file/line
or pending restart, unless an eligible captured provider supplies a value.
Startup/sighup providers may supply only normalized equivalents of the immutable
initial value, including permissions `511`/`0777` and authentication timeout
`60s`/`1min`. User defaults are captured once per physical session; RESET does not
reenter the provider and replacement captures fresh defaults.

Exactly ten names restrict reads: `unix_socket_directories`, `krb_server_keyfile`,
`oauth_validator_libraries`, `ssl_ciphers`, `ssl_dh_params_file`, `ssl_groups`,
`ssl_max_protocol_version`, `ssl_min_protocol_version`, `ssl_passphrase_command`
and `ssl_tls13_ciphers`. They require effective superuser or INHERIT-effective
pg_read_all_settings privileges. SET grants do not grant read authority or remove
context refusals; held statements recheck execution-time authority, and aggregate
reads omit restricted rows.

Current `unix_socket_permissions` is four-digit octal; Raw boot/reset/bounds
remain decimal (`511`, `0`..`511`). The four TCP settings expose stored requested
numeric values on SHOW, current_setting and pg_settings.setting, initially zero.
They do not probe kernel defaults. Other time settings keep human-unit SHOW
formatting and base-unit pg_settings values.

No listener, socket, TLS, authentication timer, password-hashing or warning
consumer, OAuth/Kerberos loader, passphrase command, Bonjour service or admission
limit is installed. Existing authentication, native controls and logical state
remain independent. The explanatory manual anchors `ssl_ciphers_high`,
`ssl_ciphers_medium`, `ssl_ciphers_not_anull` and `ssl_ciphers_plus_3des` remain
unknown parameters, not aliases.

## Compatibility settings

`allow_alter_system`, `array_nulls`, `backslash_quote`,
`escape_string_warning`, `lo_compat_privileges`, `synchronize_seqscans` and
`transform_null_equals` are setting-only simulations. Their released metadata
and typed values do not enable ALTER SYSTEM, array-input changes, lexer warnings
or rejection, large-object privilege bypass, synchronized scans or null-comparison
rewriting. No configuration file, reload, restart or process operation is implied.
`standard_conforming_strings` retains its real per-command parser consumer;
`quote_all_identifiers` retains its real SQL-fragment presentation consumer.

All nine are unrestricted reads. `lo_compat_privileges` writes require superuser
or effective parameter SET privilege, not ALTER SYSTEM or read-all-settings
membership. RESET ALL restores its captured default even after SET is revoked;
individual RESET rechecks current authority.

The builtin-only `Sighup` class projects SQL context `sighup`.
`allow_alter_system` remains on: all individual live mutations fail 55P02
“cannot be changed now” before value parsing, regardless of grants. RESET ALL
skips it. A native default provider may supply a normalized same-value alias
with source `client`, but cannot change its initial value; custom registration
cannot select Sighup or StartupOnly. The other eight settings capture trusted
typed provider defaults normally, without provider reentry on RESET.
`backslash_quote` exposes only `safe_encoding,on,off`; exact case-insensitive
hidden aliases true/yes/1 and false/no/0 canonicalize to on/off. Unlike Boolean
settings, enum prefixes and padded values are rejected.

## Replication settings

`replication.go` supplies 25 replication descriptors alongside the existing
`track_commit_timestamp`: `hot_standby`, `hot_standby_feedback`,
`idle_replication_slot_timeout`, `max_active_replication_origins`,
`max_logical_replication_workers`, `max_parallel_apply_workers_per_subscription`,
`max_replication_slots`, `max_slot_wal_keep_size`, `max_standby_archive_delay`,
`max_standby_streaming_delay`, `max_sync_workers_per_subscription`,
`max_wal_senders`, `output_plugin_libraries`, `primary_conninfo`,
`primary_slot_name`, `recovery_min_apply_delay`, `sync_replication_slots`,
`synchronized_standby_slots`, `synchronous_standby_names`, `wal_keep_size`,
`wal_receiver_create_temp_slot`, `wal_receiver_status_interval`,
`wal_receiver_timeout`, `wal_retrieve_retry_interval` and `wal_sender_timeout`.
The registry contains 26 unique family names: six postmaster, eighteen sighup,
one user and one superuser context. Immutable contexts reject individual live
changes before parsing or authority checks; RESET ALL skips them.

Numeric values use the shared int32 parser and inclusive release bounds:
worker/slot/origin counts cap at262143, parallel apply workers at1024,
receiver status seconds at2147483, and other integers at2147483647.
Retry interval starts at1; standby delays and slot WAL size allow-1.
WAL sizes use MB, idle-slot/receiver-status intervals use seconds, and the
remaining durations use milliseconds. SHOW formats units; catalog values use
base units. Boot/reset strings are non-NULL, including empty strings.

Only primary_conninfo and output_plugin_libraries restrict examination;
effective read-all-settings authority is independent of parameter SET authority.
wal_sender_timeout is user-settable; output_plugin_libraries uses effective
superuser/parameter SET authority. Its SQL SET list quotes identifier arguments,
while set_config preserves raw text, without validating or loading libraries.
set_config also requires examination authority for its returned value; a denied
return leaves the previous override/default/source intact. SQL SET needs no
examination permission.
synchronous_standby_names and synchronized_standby_slots flatten raw SET lists.
Providers may supply only normalized equivalents for immutable defaults;
mutable defaults are captured once per physical session. LOCAL/savepoints,
RESET/default, session isolation and reuse share the ordinary settings store.
Logical Reset/Dump/Restore neither clear nor serialize live settings.

The additions are setting-only simulations, not WAL generation/retention, slots,
origins, workers, connections, feedback, replay delay, synchronous waiting,
timeouts, restarts or plugin loading. Logical constraints remain active.
track_commit_timestamp retains its genuine native timestamp consumer and driver
configuration; in_hot_standby is unchanged.

## What stays in the engine

SET/RESET/SHOW execution, `current_setting`/`set_config` and RESET ALL stay
in the engine (see the
[engine README](../core/README.md#immutable-settings-registry)); the `pg_settings`
producer is in `pgcatalog`. Per-session
storage and the SET LOCAL action stack (`SettingsStore`), command admission,
timeout publication, notice levels and emission, and the presentation
capture, definition lookup, value parse, SET input and access-class check
live in [`session`](../session/README.md).

## Testing

Package units cover typed input and formatting, the normalizers, search-path
parsing, registry metadata, simulated startup defaults and raw integer/real formatting:

```sh
make check RUN='^Test(ParseAndFormatTypedInput|EncodingLocaleAndLevelNormalizers|ApplicationNameNormalizer|ParseSearchPath|SettingsMetadata(Raw|Registry)|PhysicalSettingsDefinitions)$$'
make check RUN='^Test(ClientSettings(BootAndStartup|KeywordLists|ProviderCapture)|ClientCatalogNameNormalization|ClientPrivilegedResetAll|Settings(XMLOption|ClientListFlattening)|SettingAccessAllowedCallback)$$'
make check RUN='^TestCompatible(Settings(Definitions|ProviderCapture|ResetAll)|BackslashQuoteNormalization)$$'
make check RUN='^TestConnectionSettings'
make check RUN='^TestDeveloperSettings'
make check RUN='^TestLoggingSettings'
make check RUN='^TestLockSettings'
make check RUN='^TestReplicationSettings'
```

Public metadata, registered-custom and SHOW scenarios live in
[reference/session_state](../../../reference/session_state/README.md#testing).
Their `SHOW ALL` expectations assert every built-in description independently
against PostgreSQL 18 `short_desc`, including the current and default
transaction settings, and every registered custom description, with no
empty-string exemptions.

