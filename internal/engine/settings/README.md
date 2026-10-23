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
  follows PostgreSQL18 English wording. `Validate` captures custom category
  `Customized Options`, dotted names, bounds, enums and exact typed boot values;
  `BootValue`, `FoldName`, `CustomName`, `DisplayName` and `SessionDefaultProvider`.
  Validation errors use the same `"stuntgres: "` prefix as the engine's `RegistrationError`,
  which stays in the engine because foreign-data registration shares it.
- **Builtins** (`builtins.go`, `messages.go`, `format_encoding.go`): the
  private builtin table in PostgreSQL 18.0 `short_desc` wording, the
  `application_name`, `client_min_messages` (`ClientMinMessages`),
  encoding, locale and `icu_validation_level` normalizers. `MessageLevelRank`
  orders the client message levels; the `session.NoticeLevel` constants
  follow that order.
- **Values** (`value.go`, `number.go`, `units.go`): `Value` and
  `Definition.Parse`/`Format` and base-unit `Raw`, PostgreSQL-compatible number
  scanning (`ParseNumber`, also used for storage options) and memory/time units.
- **Search path** (`search_path.go`): the pure list parser. Namespace and ACL
  resolution are in `analyze` (`search_path.go`).
- **Registry** (`registry.go`): `NewRegistry(registered,
  trackCommitTimestamps)` captures builtin boot values (the engine passes its
  commit-timestamp tracking flag), adds validated registered definitions and
  sorts the names. `Lookup`, `Definitions` and `Order` read it.

## Invariants

- **Immutable registry.** A `Registry` is built once and never changed. The
  engine holds one by value (`Engine.settings`) and parameter providers
  borrow a pointer to it. `Definitions`, `Order`, enum values and boot
  `Value.Path` slices are borrowed without copies; callers must not mutate
  them. Reads need no lock.
- **No callbacks or state.** Nothing here calls default providers, reads a
  session, takes a lock or emits notices. Normalizers return a notice for
  the caller to emit.
- **Stable output.** Builtin order, boot values and descriptions, validation
  messages, SQLSTATEs, hints and unit conversions are compatibility
  contracts; `SHOW ALL` and the reference suite observe them.

## What stays in the engine

SET/RESET/SHOW execution, `current_setting`/`set_config`, RESET ALL and the
`pg_settings` producer. Per-session storage and the SET LOCAL action stack
(`SettingsStore`), command admission, timeout publication, notice levels and
emission (13d.6), and the presentation capture, definition lookup, value
parse, SET input and access-class check (13g) live in
[`session`](../session/README.md).

## Testing

Follow the [shared testing guide](../../../docs/TESTING.md). Run from the
repository root.

Pure definition behavior (typed input and formatting, normalizers,
search-path parsing):

```sh
make check RUN='^Test(ParseAndFormatTypedInput|EncodingLocaleAndLevelNormalizers|ApplicationNameNormalizer|ParseSearchPath)$'
```

Engine-side session, publication and public SQL selectors are in the
[engine README](../README.md#immutable-settings-registry).

Registry metadata and raw integer/real formatting are supplemented by:

```sh
make check RUN='^TestSettingsMetadata(Raw|Registry)$'
```

The shared public metadata, registered-custom and actual feature-smoke scenarios
live in [reference/session_state](../../../reference/session_state/README.md#runtime-settings-metadata);
run their selected definitions in native and pinned PostgreSQL modes.

