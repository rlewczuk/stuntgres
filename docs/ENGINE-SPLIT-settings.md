# Engine split, step 8: `internal/engine/settings`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-settings.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 8; §4.1 pattern 1
  (state ownership in place); §3 (L1).
- **Needs:** nothing. Read the lessons in
  [`ENGINE-SPLIT-storage.md`](ENGINE-SPLIT-storage.md) (6a/6b: store, then
  move) and [`ENGINE-SPLIT-catalog.md`](ENGINE-SPLIT-catalog.md) (5f: alias
  removal tools).
- **Effort:** high for 8a and 8b; medium for 8c.
- **Session size:** one sub-step per session.

## Goal

Setting definitions, values, parsing, formatting and validation become a leaf
package that imports only `sqltype`, `sqlstate` and the standard library. The
per-session store and SET/SHOW execution stay in the engine for step 13.

## Inventory (measured at `75fe0d710`)

Re-measure first: `master` moves fast, and `PG18-ADMIN-SETTINGS-*` parts add
builtin settings.

| File | Lines | Content | Clean (§2.6) |
|---|---:|---|---|
| `settings.go` | 695 | `SettingDefinition` (`:50`), `SettingType`/`SettingAccess`/`SettingUnit` and constants, `SessionDefaultProvider` (`:77`), `builtinSettings` (`:86`), `RegistrationError` (`:121`, a function), `ValidateSetting` (`:157`), `settingValue` (`:263`), `parse` (`:281`), `format` (`:662`), units (`unitTable`, `memoryUnits`, `timeUnits`, `convertSettingUnit`) | 611 of 639 declaration lines |
| `settings_format_encoding.go` | 90 | encoding and locale normalizers, `icuValidationLevel` | all |
| `notices.go` | 94 | `clientMinMessages` (a definition, `:26`), `normalizeClientMinMessages` (`:53`), `noticeLevel` (`:11`, used by `Operation`) | definitions clean |
| `schema_path.go` | 360 | `parseSearchPath` (`:16`, pure, 63 lines); the rest is search-path resolution | `parseSearchPath` clean |
| `parameter_catalog.go` | 83 | `parameterProvider{definitions}`: validates parameter ACL descriptors against the definitions | clean |

Dirty declarations in `settings.go`, all three on `Engine`:

- `initializeSettings` (`:196`) builds `settingsDefinitions` and
  `settingsOrder` and reads `e.visibility.TracksCommitTimestamps()` to boot
  `track_commit_timestamp`.
- `settingDefinitions` (`:218`) returns the map.
- `setting` (`:219`) looks one definition up.

Engine state: `Engine.settingsDefinitions map[string]SettingDefinition` (16
uses in 10 files) and `Engine.settingsOrder []string` (7 uses in 4 files).
Both are written only by `initializeSettings`, during `newEngine`.

Direct reads of the unexported `bootValue` outside `settings.go`:
`schema_path.go:85` and `sequence_prepared.go:50` (both `search_path`), and
`session_settings.go:192` (`initialize`).

Other definition literals outside `settings.go`: `index_options.go:77`
(parses a boolean storage option through `SettingDefinition.parse`),
`roles.go:22` (`role` session default), `settings_format_encoding.go`, and
`session_settings.go:270/273` (custom placeholders).

Users outside the package (keep them compiling):

- the root driver: `settings.go` mirrors the four public types and calls
  `engine.ValidateSetting` and `engine.RegistrationError`; `driver.go:32/68`
  passes `[]engine.SettingDefinition` to `engine.NewWithSettings`; `fdw.go`
  calls `engine.RegistrationError`;
- `cmd/stuntgres/database.go:25/32` implements `SessionDefault(ctx,
  engine.SettingDefinition)`; `cmd/stuntgres/startup.go:328` calls
  `Session.Setting`.

## Sub-steps

### 8a — Registry in place

1. Add an engine-local `settingsRegistry` (new file `settings_registry.go`)
   with `definitions map[string]SettingDefinition` and `order []string`.
   `Engine` holds it by value as `settings` and never replaces it. Document
   that it is immutable after `newEngine`, so reads need no lock. Confirm that
   with a grep: no write after construction.
2. Replace `initializeSettings` with a pure constructor
   `newSettingsRegistry(registered []SettingDefinition, trackCommitTimestamps
   bool) settingsRegistry`. `newEngine` passes
   `e.visibility.TracksCommitTimestamps()`. Keep the panic for invalid builtin
   boot values and the sort of `order`.
3. Turn `Engine.setting` and `Engine.settingDefinitions` into registry methods
   (`Lookup(name)`, `Definitions()`, `Order()`), and rewrite the 23 field uses
   with a `go/types` selector tool. Keep error values and messages identical.
4. Add `SettingDefinition.BootValue() settingValue` and use it at
   `schema_path.go:85`, `sequence_prepared.go:50` and
   `session_settings.go:192`.
5. Move `parseSearchPath` from `schema_path.go` and the definition half of
   `notices.go` (`clientMinMessages`, `normalizeClientMinMessages`) into
   settings-owned files (for example `settings_search_path.go`,
   `settings_notices.go`). `noticeLevel` stays in `notices.go` if `Operation`
   needs it; the normalizer then returns the level text only. Check before
   moving.
6. `parameterProvider` takes the registry instead of a raw map
   (`engine.go:201`, `pg_parameter_acl.go:28`, `catalog.go:577`).

No behavior change. One commit per numbered group is fine.

### 8b — Create `settings` and move

1. Decide the names with the user (§7 decision 6) and record them here. The
   proposal: `Definition`, `Type`, `Access`, `Unit`, `Value`, `Registry`,
   `Validate` (today `ValidateSetting`), `Builtins` (today `builtinSettings`)
   and `SessionDefaultProvider`. The constants lose their `Setting` prefix
   (`settings.Boolean`, `settings.Kilobytes`, `settings.Session`).
2. Export what the engine needs: `Parse`, `Format`, `Invalid`, `BootValue`,
   the `Value` accessors the engine reads (`Text`, `Path`, `Zone`,
   `DateStyle`, `Locale`, ...), `FoldName` (today `settingFold`) and
   `CustomName`. Use the type-checked rename approach of 6b: export in place
   first, then move with the 6b `movex` approach.
3. `RegistrationError` is a function shared with FDW registration and named
   by the root driver. Keep it in the engine. `settings` gets a private helper
   with the same message prefix (`"stuntgres: "`), so validation errors read
   the same.
4. Move the declarations listed in the inventory, plus the registry. Leave
   unexported aliases in the engine (`settingValue = settings.Value`, ...) and
   exported aliases for the public types (`SettingDefinition =
   settings.Definition`, ...), so the root driver and `cmd/stuntgres` compile
   unchanged.
5. Check `go list -deps`: `settings` imports only `sqltype`, `sqlstate` and
   the standard library.

### 8c — Remove the aliases

1. Rewrite engine uses to `settings.` names with the 5f/6c alias tool
   (census before and after, shadowing audit for locals named `settings`;
   `Session.settings` is a field and does not collide).
2. Switch the root driver (`settings.go`, `driver.go`) and
   `cmd/stuntgres/database.go` to import `internal/engine/settings`
   directly, as they already do for `catalog` and `ids`.
   `engine.NewWithSettings` takes `[]settings.Definition`. Root mirror types
   in `settings.go` stay: they are the public API.
3. Delete the aliases.

## Stays in the engine (for step 13)

`sessionSettings` and its action stack (`session_settings.go`: `settingAction`,
`touch`, `touchRole`, `finishTransaction`), `executeSetting`, `applySetting`,
`sessionSetting`, `Session.Setting`, `setting_calls.go` (`current_setting`,
`set_config`), `setting_admission.go`, `settingsMark` (`savepoints.go`),
`sessionPresentation` (`transaction.go`), `resetAllSettingsLocked`
(`discard.go`) and `publishTimeoutSettingsLocked` (`session_timeout.go`). They
read `Session`, `Operation`, session authorization or the transaction owner.

## Invariants

- Builtin order, boot values and descriptions stay byte-identical. `SHOW ALL`
  output, including the descriptions that `AGENTS.md` requires to match
  PostgreSQL 18.0 `short_desc`, must not change.
- Validation messages, SQLSTATEs, hints and unit conversions stay identical.
- Registration still rejects invalid and duplicate definitions with the same
  messages, and custom names must still be dotted.
- `track_commit_timestamp` still boots from the engine's tracking flag.

## Targeted tests

- Engine unit tests over settings: grep `*_test.go` for `SettingDefinition`,
  `ValidateSetting`, `settingsDefinitions`, `builtinSettings` and
  `parseSearchPath`. Those that test pure definition behavior move with the
  code in 8b.
- Reference selections (quick mode and `REFERENCE=1`): grep `reference/` for
  `SHOW ALL`, `current_setting`, `set_config`, `SET LOCAL`, `search_path`,
  `DateStyle`, `TimeZone`, `client_min_messages`, `RegisterSetting`. Never
  edit `reference/`.
- `make bench` after 8a and 8b: settings are built during engine startup.
- `make check RUN='...'` for the root driver's registration tests (grep the
  repository root for `RegisterSetting`).

## Documentation

- Create `internal/engine/settings/README.md` (8b): purpose, what it owns,
  the immutability rule for the registry, what stays in the engine,
  `## Testing` with selectors moved from `internal/engine/README.md`.
- `docs/SETTINGS.md` names internal types; update it where it does.
- `AGENTS.md`: add `settings` to the shared subpackage bullet. The "SHOW ALL
  built-in descriptions" rule stays in `AGENTS.md` (it is a test rule).
  Delete nothing else; the file must not grow.

## Done when

`settings` imports only `sqltype`, `sqlstate` and the standard library.
`Engine` holds one registry instead of two fields. The root driver and
`cmd/stuntgres` use `settings.` names. The checks in §8.3 pass with no
startup regression.

## Status

- [ ] 8a — Registry in place
- [ ] 8b — `settings` created and definitions moved
- [ ] 8c — Aliases removed

Outstanding aliases: none yet.

Notes for later steps:

- Step 13 decides where `sessionSettings` goes (§7 decision 10). It reads
  `Session.authorization` (role settings) and calls
  `publishTimeoutSettingsLocked`, so it cannot move into `settings` as is.
- Step 9 (`pg_parameter_acl`) reads the registry; 9b needs 8b.
