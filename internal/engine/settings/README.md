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
- **Builtins** (`builtins.go`, `messages.go`, `format_encoding.go`): the
  private builtin table in PostgreSQL 18 English `short_desc` wording, the
  `application_name`, `client_min_messages` (`ClientMinMessages`),
  encoding, locale and `icu_validation_level` normalizers. `MessageLevelRank`
  orders the client message levels; the `session.NoticeLevel` constants
  follow that order.
  `plpgsql.check_asserts` is an ordinary user-settable Boolean, booting on, with
  `Customized Options` metadata. Procedural execution reads its effective session
  value; registry construction does not capture an assertion decision.
- **Values** (`value.go`, `number.go`, `units.go`): `Value` and
  `Definition.Parse`/`Format` and base-unit `Raw`, PostgreSQL-compatible number
  scanning (`ParseNumber`, also used for storage options) and memory/time units.
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
authority. This does not unlock deny-only custom/logging definitions.

The internal trusted native SessionDefaultProvider retains its pure typed
capture contract, not a PostgreSQL startup/catalog hook. AM/tablespace/FTS
provider strings may name nonexistent simulated objects; this is not evidence
those objects exist. Explicit SQL assigning an unknown name still fails.
DEFAULT/RESET restores the captured value without provider reentry or catalog
lookup. Providers still use pure parsing/normalization (including name clipping)
with silent initialization; replacement physical sessions capture defaults anew.


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

## What stays in the engine

SET/RESET/SHOW execution, `current_setting`/`set_config` and RESET ALL stay
in the engine (see the
[engine README](../README.md#immutable-settings-registry)); the `pg_settings`
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
```

Public metadata, registered-custom and SHOW scenarios live in
[reference/session_state](../../../reference/session_state/README.md#testing).
Their `SHOW ALL` expectations assert every built-in description independently
against PostgreSQL 18 `short_desc`, including the current and default
transaction settings, and every registered custom description, with no
empty-string exemptions.

