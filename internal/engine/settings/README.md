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
```

Public metadata, registered-custom and SHOW scenarios live in
[reference/session_state](../../../reference/session_state/README.md#testing).
Their `SHOW ALL` expectations assert every built-in description independently
against PostgreSQL 18 `short_desc`, including the current and default
transaction settings, and every registered custom description, with no
empty-string exemptions.
