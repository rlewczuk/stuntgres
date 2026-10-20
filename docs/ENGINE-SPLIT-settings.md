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

This table is historical. 8b moved the definition code into
[`internal/engine/settings`](../internal/engine/settings/README.md); see
[Status](#status). Before that, Settings8a kept definitions, values and helpers
in the engine: `settings_registry.go` owns construction/lookup/borrowed metadata,
`settings_search_path.go` owns the pure parser, and `settings_notices.go` owns
the notice setting definition and text normalizer. Runtime path resolution and
notice emission remain in their original files. `parameterProvider` borrows a
pointer to the same Engine-owned registry value without mutation or replacement.
The former Engine fields and lookup methods are removed; the current
migration/evidence record is in [Status](#status).

Re-measure first: `master` moves fast, and `PG18-ADMIN-SETTINGS-*` parts add
builtin settings (`row_security` landed at `2205fe385`, together with the
`pg_settings` producer in `pg_settings.go`, which reads `sessionSettings` and
belongs to step 9).

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

Historical Engine state was split between `settingsDefinitions` and
`settingsOrder`. The pre-8a semantic census found 35 definitions selectors,
12 order selectors and 9 legacy method calls across 23 Go files. Besides the
constructor, the private normalizer/publication witness wrote the map; 8a
replaces that setup with a definition installed before construction.

`SettingDefinition.BootValue()` now supplies exact typed defaults to
`schema_path.go`, `sequence_prepared.go` and `session_settings.initialize`.
Settings-owned construction and exact-payload units may inspect private storage.

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
   that it is immutable after construction, so reads need no lock. Confirm with
   a typed selector/write census plus manual alias and borrowed-storage tracing.
2. Replace `initializeSettings` with a pure constructor
   `newSettingsRegistry(registered []SettingDefinition, trackCommitTimestamps
   bool) settingsRegistry`. `newEngine` passes
   `e.visibility.TracksCommitTimestamps()`. Keep the panic for invalid builtin
   boot values and the sort of `order`.
3. Turn `Engine.setting` and `Engine.settingDefinitions` into registry methods
   (`Lookup(name)`, `Definitions()`, `Order()`), and rewrite semantic consumers
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
6. `parameterProvider` borrows `*settingsRegistry` instead of a raw map, while
   Engine retains its single value (`engine.go:201`, `pg_parameter_acl.go:28`,
   `catalog.go:577`).

No behavior change. One commit per numbered group is fine.

### 8b — Create `settings` and move

1. Decide the names with the user (§7 decision 6) and record them here
   (decided in 8b: the proposal, as below; `Value` has exported fields). The
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
  `ValidateSetting`, `settings.Definitions`, `builtinSettings` and
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

- [x] 8a — Registry in place (`3c1e1115a`)
- [x] 8b — `settings` created and definitions moved (`8057bb8d0`,
  `dbf23631c`, `cc31fb6f6`, then the docs commit) *(racecheck skipped at user
  request)*
- [ ] 8c — Aliases removed

Outstanding aliases (`internal/engine/settings_aliases.go`, removed in 8c):
types `SettingType`, `SettingAccess`, `SettingUnit`, `SettingDefinition`,
`SessionDefaultProvider`, `settingValue`, `settingsRegistry`; constants
`SettingBoolean`, `SettingString`, `SettingInteger`, `SettingFloating`,
`SettingEnum`, `SettingSession`, `SettingReadOnly`, `SettingPrivileged`,
`settingStartupOnly`, `SettingUnitNone`, `SettingBytes`, `SettingKilobytes`,
`SettingMegabytes`, `SettingMilliseconds`, `SettingSeconds`, `SettingMinutes`;
and the wrapper function `ValidateSetting`. `RegistrationError` is not an
alias: it stays in the engine (`foreign_registry.go`).

8b results:

- **Decisions (user).** The proposed names: `Definition`, `Type`, `Access`,
  `Unit`, `Value`, `Registry`/`NewRegistry`, `Validate`,
  `SessionDefaultProvider`; constants without the `Setting` prefix
  (`StartupOnly` is now exported). `Value` exposes exported fields (`Text`,
  `Number`, `Boolean`, `Path`, `Zone`, `DateStyle`, `Locale`,
  `TimezoneAbbreviations`), as 5d/6b did. `settings` owns the client message
  level names (`MessageLevelRank`); `noticeLevel` and its constants stay in
  the engine (about 60 files emit notices), and `messageLevel` converts the
  rank.
- **Re-measure** (at `4d3568a9d`, `go/types` census over the five definition
  files). Only two references led back into the engine: `sqlError` in
  `Lookup` and `messageLevel` in the `icu_validation_level` normalizer.
  Outside uses: 74 `.Name`, 60 `SettingDefinition`, 35 `.number`, 26
  `Definitions`, 18 `settingValue`, 15 `.parse`, and smaller counts.
- **Self-contained in place** (`8057bb8d0`). `Lookup` builds its 42704 error
  directly, validation uses a private `registrationError` with the same
  prefix, and `messageLevelRank` replaces the `messageLevel` call.
  `RegistrationError` moved to `foreign_registry.go`.
- **Export in place** (`dbf23631c`). A type-checked rename: `parse`/`format`
  became `Parse`/`Format` and the eight `settingValue` fields were
  capitalized (121 references in 26 files).
- **Move** (`cc31fb6f6`). The same tool renamed the move set to the final
  names and rewrote engine call sites of moved functions to `settings.X`
  (`FoldName` 14, `ClientMinMessages` 6, `DisplayName` 5, `CustomName` 4,
  `IsLocaleSetting` 4, `MessageLevelRank` 3, `ParseNumber` 3, `NewRegistry`
  2; counts include move-set declarations). Locals named `settings` in
  those scopes became `store` or `registry`. `settings.go` was split into
  `definition.go`, `builtins.go`, `value.go`, `number.go` and `units.go`;
  the other files are `format_encoding.go`, `messages.go`,
  `search_path.go` and `registry.go`. `builtinSettings` became the private
  `builtins` rather than the proposed exported `Builtins`: no engine code
  reads it, and an exported slice could be mutated. `Invalid` also stays
  private for the same reason. `settings` imports only `sqlstate`,
  `sqltype` and the standard library.
- **Tests.** Moved to `settings`: `TestSettingTypedInput` →
  `TestParseAndFormatTypedInput`, `TestFormatEncodingNormalizers` →
  `TestEncodingLocaleAndLevelNormalizers`, `TestNoticesApplicationName` →
  `TestApplicationNameNormalizer`, and the `parseSearchPath` table of
  `TestSchemaSearchPathSessionDefaultsAndSpecials` → `TestParseSearchPath`.
  Adapted in the engine: `TestSettingCapturedDefaults` (expected value via
  `Lookup`/`Parse`), `TestSettingExactDecimalDefaultCapture` (`BootValue()`),
  `TestNoticesFilter` (`ClientMinMessages.Normalize`),
  `TestTransactionUtilityCompletionBoundary` (literal `application_name`),
  `TestFormatEncodingCapturedDefaults` (local renamed). None deleted.
- **Verification.** `make build`, `go vet` and targeted `make check`
  selections (settings, notices, format encoding, schema/search path,
  savepoints, discard, timeouts, transactions, foreign registration,
  parameter ACL, row security and the new `settings` tests) passed after each
  commit. `make quickcheck` passed (exit 0, 1m50s). Reference selections
  `^TestReferenceSettings` (36 definitions) and a 30-family selection
  (format/encoding, notices, search path, bytea output, parameter ACL, role
  settings, time zone/DateStyle/IntervalStyle, timeouts, transaction
  settings, parallel and numeric settings, savepoints, DISCARD, monitoring,
  encoding, setting-call authority) passed in quick mode and with
  `REFERENCE=1` against the pinned PostgreSQL 18.6 image. A public driver
  smoke (registration errors, typed custom units and enums, SET/SHOW of
  search_path, client_min_messages, TimeZone, DateStyle,
  icu_validation_level, rejected values, `current_setting`, `set_config`,
  `SHOW ALL` order and descriptions) printed byte-identical output on
  `4d3568a9d` and the branch. `make deps` lists only module paths; no
  subpackage imports the engine. `make bench` (`BenchmarkOpenDatabase`, five
  runs, load average about 30): `4d3568a9d` 1.30–1.53 ms/op, 5,100 steady
  allocations (one cold 5,110); branch 1.25–1.42 ms/op, 5,100 (one cold
  5,108). No allocation change; the <1 ms target is missed on master too.
  `make racecheck` and `finalcheck` were skipped at the user's request.
  `AGENTS.md` stays at 100 lines.

Notes for 8c: engine files that now import `settings` and also use locals
named `settings` were renamed (`store`, `registry`); the alias rewrite must
audit the remaining `settings :=` locals in `session_settings.go` and tests
(`Session.settings` and `Engine.settings` are fields and do not collide).

8a implementation/proof evidence (root observations; review/integration pending):

- Root executed the temporary standard-library `go/ast` + `go/types` census and
  selector migration. Corrected census: `Engine.settingsDefinitions` 32 reads /
  3 writes; `settingsOrder` 11 reads / 1 write; `initializeSettings` 1 call,
  `settingDefinitions` 1 call, `setting` 7 calls; five declarations, one each.
  The 47 field selectors plus 9 method calls supersede the old 23-use estimate.
  Production and same-package test variants were loaded; no ignored or cgo Go
  files were reported. An initial helper deduplication bug omitted nested
  selectors; the corrected census, not that initial output, drove migration.
- `Engine.settings` is assigned at the former initialization boundary before
  provider registration. The independent constructor preserves builtin parsing,
  panic and sorting, uses the supplied tracking Boolean, and inserts validated
  custom typed defaults without reparsing them. Accessors borrow immutable
  maps/slices; no read-time copying, locks, callbacks or placeholder insertion.
- Manual borrowing trace: session initialization reads the map/order and stores
  typed values in separate session maps; provider enum metadata is defensively
  copied before callback. Lookup/membership/SHOW/reset/characteristic consumers
  only read definitions. Parameter provider holders borrow `&Engine.settings`;
  validation dereferences it only for the existing read-only canonicalization
  helper. No registry is allocated separately or written through that pointer.
  Boot paths are retained as immutable values; path resolution ranges over
  entries and builds a separate address result.
  Parsing creates a fresh path; private path overrides assign that new value to
  session storage. Enum mutation in the ownership witness targets only the
  provider's defensive copy. Constructor-local map insertion/order append/sort
  are the intended registry writes. The refreshed typed audit in `tests-3.md`
  reports zero legacy declarations/selectors, `Engine.settings` 46 reads /
  1 write, `BootValue` 3 calls, `Definitions` 25 calls, `Lookup` 7 calls and
  `Order` 9 calls. Registry definitions have 2 reads / 1 write and order has
  3 reads / 1 write. The semantic audit plus manual alias/escape trace covers
  the five provider address-taking sites, borrowed map/order/enum/path storage,
  construction-local writes and callback-owned enum copies; no postpublication
  mutation or replacement was identified.
- Parameter provider registration, catalog validation, ACL capture and grant
  canonicalization share that registry. Sparse descriptor identity and error
  precedence, bounded row_security-only `pg_settings`, runtime session state and
  publication locks are unchanged.
- No units moved or were deleted. `TestSettingsLocalNormalizerPublication` is
  substantially rewritten only in setup/name: a validated `demo.normalizer`
  definition with boot `b` gets its callback before construction. Only staged
  `transient` input triggers real cancellation, Reset or three-lock inspection.
  Original cancellation/Finish, successful result, cleanup and owner-default
  assertions remain. Other private units use the migrated selectors.
- Root attempted LSP references but no language server was available. The
  temporary helper supplies semantic selector/type references instead; public
  root and cmd adapters retain their existing types and signatures.
- Root reports are retained under
  `.cswd/parts/PG18-ENGINE-SPLIT-SETTINGS-REGISTRY/`.
  `tests-2.md` records the initial cutover's typed audit, build, U1–U5,
  R1–R9 in native and pinned PostgreSQL18.6 modes, actual public API smoke,
  deps and downward-layering checks passing. Whole quickcheck initially failed
  at `TestReferenceTimeoutTransactionAbortRestore/statement/child=false/memory`,
  `timeout_transaction_test.go:363`, with unexpected statement timeout
  (exit2, wall354.29s); chained racecheck was not reached. This was in private
  transaction preparation, not the intended blocked-command timeout assertion;
  retained output does not identify the exact preparation SQL command.
- The narrow provider-pointer remedy preserves the Engine-owned registry value
  and changes only provider borrowing and its literals. `tests-3.md` records
  fresh build, typed audit, U3 with `RACE=1`, R8/R9 in both native and pinned
  modes, the same public API smoke and full quickcheck passing. Unaffected
  U1/U2/U4/U5 and R1–R7 proof remains in `tests-2.md`. The smoke observed
  registered boot/reset, live held reads, two physical sessions' isolation,
  SHOW ALL TEXT metadata/descriptions/order, quoted search-path resolution,
  notice filtering/info/debug and constructor tracking off/on. Parameter
  grants/authority are exercised by R8/R9, not inferred from registration.
  The initial timeout failure remains recorded, not claimed causally fixed.
- The default full race run in `tests-3.md` failed four context-deadline
  scenarios: `TestReferenceObjectAddressAuthorityRoutines`,
  `TestReferenceStringCoreReview2DeparseSourceConsumerRoutes`,
  `TestReferenceInfoSQLIdentifierNativeControls` and
  `TestReferenceInfoYesOrNoNativeControls`. Its chained deps command was not
  reached; passing dependency/layering evidence is from `tests-2.md`.
  `tests-4.md` records `GOMAXPROCS=4 make racecheck` unfinished at the external
  900s deadline (wall900.06s): emitted packages passed, but this is not a suite
  pass. `tests-5.md` records `GOMAXPROCS=8 make racecheck` exit0,
  wall896.52s within the same900s deadline, with all packages passing.
  No production/test timeout, assertion or coverage changes were made.
- Observed host load averages were 134.43/119.65/81.17 before remedy proof,
  then 116.73/147.15/143.84. **[INFERENCE]** Scheduling contention is consistent
  with the timing/deadline variability, but load observations do not establish
  its cause or exclude a latent timeout issue. The concurrency override is
  evidence of a passing run, not a guaranteed repair.
- Five fresh-engine startup samples per round, from `make bench`
  (`BenchmarkOpenDatabase`; ns/op, B/op, allocs/op):

  |Round / sample|ns/op|B/op|allocs/op|
  |---|---:|---:|---:|
  |Baseline tests-1 / 1|985389|1659995|5160|
  |Baseline tests-1 / 2|1169671|1656222|5152|
  |Baseline tests-1 / 3|1234896|1657757|5152|
  |Baseline tests-1 / 4|1411015|1656222|5152|
  |Baseline tests-1 / 5|1778083|1655435|5152|
  |Initial cutover tests-2 / 1|1284128|1658681|5162|
  |Initial cutover tests-2 / 2|1137240|1658000|5154|
  |Initial cutover tests-2 / 3|1137967|1656450|5153|
  |Initial cutover tests-2 / 4|2142314|1659796|5153|
  |Initial cutover tests-2 / 5|2663370|1654709|5153|
  |Pointer remedy tests-3 / 1|18646758|1625522|5224|
  |Pointer remedy tests-3 / 2|12679016|1641938|5151|
  |Pointer remedy tests-3 / 3|4513226|1624894|5150|
  |Pointer remedy tests-3 / 4|7382864|1645059|5151|
  |Pointer remedy tests-3 / 5|5784624|1632096|5150|

  Stable allocation observations are baseline5152 → interim5153 →
  remedy5150–5151, with a cold remedy sample5224. **[INFERENCE]** Boxing the
  former four-word registry inside the interface-held provider may explain
  the interim +1 allocation; no compiler allocation trace establishes that
  mechanism. Fresh samples no longer show that stable allocation regression,
  but do not establish guaranteed allocation recovery on every run. Four
  baseline samples already missed the <1ms target; all remedy samples miss it
  (4.51–18.65ms). These timings do not demonstrate a speedup or satisfaction
  of the startup target, nor isolate a registry-attributable timing change.
- Only 8a implementation/proof is complete. Independent review and integration
  remain pending; `make finalcheck` is pending postintegration, not a recorded
  success. Root will refresh quickcheck and smoke after these documentation
  edits before review. No permanent tooling, reference/smoke edits or new
  settings package are part of this refactor; 8b/8c remain untouched.


Notes for later steps:

- Step 13 decides where `sessionSettings` goes (§7 decision 10). It reads
  `Session.authorization` (role settings) and calls
  `publishTimeoutSettingsLocked`, so it cannot move into `settings` as is.
- Step 9 (`pg_parameter_acl`) reads the registry; 9b needs 8b.
