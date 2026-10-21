# Engine split, step 21: `internal/engine/regobj`

Session prompt. Start a fresh session with "Implement
`docs/ENGINE-SPLIT-regobj.md`." Follow the shared rules in
[ENGINE-SPLIT.md §8](ENGINE-SPLIT.md#8-implementation-sessions): protocol,
constraints, verification, documentation and report.

- **Plan:** [ENGINE-SPLIT.md](ENGINE-SPLIT.md) §6 step 21; §3 (layer L11,
  rule 6: `regobj` is imported by the upper features, `stored` and the
  root, never by `analyze`, `exec`, `session`, `pgcatalog` or the lower
  features); §4.1 patterns 1 and 3; the tests rule in §4.
- **Needs:** 21a: nothing (linear item 64). 21b: 21a and catalog 5j
  (items 64 and 74; 5j gives `catalog.RulesPayload` a rule-identity
  accessor, see 21b step 1). 21c: 21b (item 65).
- **Effort:** high for each sub-step.
- **Session size:** one sub-step per session.
- **Linear items:** 21a = 64, 21b = 65, 21c = 66.
- **Tools:** `reach`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-EXEC-REFINE/reach/reach -dir . -files '<regexp>'`)
  and `frontier`
  (`.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/frontier/frontier -dir . -files a.go,b.go -decls X,Y`),
  run from the worktree root; never pipe them into `head`. Rewrite tools:
  `movex`, `unalias`, `keylit`, `shadowrn` in
  `.cswd/tmp/PG18-ENGINE-SPLIT-PLAN-ALIASES/`; `movespec`, `expspec` and
  the move driver `mv.sh` in `.cswd/tmp/PG18-ENGINE-SPLIT-ANALYZE-QUERY-PREP/`.
  The measurements below are in `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/regobj/`.

## Goal

`regobj` holds the code that writes catalog objects and typed values as
SQL text, and reads them back:

- the object-identifier types (`regclass`, `regproc`, `regprocedure`,
  `regoper`, `regoperator`, `regtype`, `regrole`, `regnamespace`,
  `regcollation`, `regconfig`, `regdictionary`): spellings, output,
  input, dump spellings, stored-value walkers and remap;
- type declarations (`format_type`-style names) and typed literals;
- the `aclitem` input and output;
- object identity: `pg_identify_object`, `pg_describe_object` and
  `pg_get_object_address`.

Today this code is spread over 32 root files, with pieces filed under
sequences, domains, visibility and routines. Almost all of it is already
clean, but views, the table family, Dump and the type dumps cannot move
until it has a package (ENGINE-SPLIT.md §2.7). After this step a reader
finds every "name of an object as SQL" rule in one place.

What stays in the root: the statement caches and formatters
(`StatementScope.Format*`, behind `exec.Host`), the `Engine.capture*`
constructors that read engine state, and the `analyze.Host` resolver
methods, which become thin forwarders.

## Inventory (measured at `c154e14fb`)

`frontier` over the 21b set (91 top-level declarations with their
methods, about 2.5k lines) reports only these root edges:

| Edge | Used by | Fix |
|---|---|---|
| `sqlError` (`engine.go`) | resolvers, identity, input helpers | the package gets its own `errors.go` copy, as every subpackage does |
| `remapRegclassDatum` (`domain_remap.go`) | `regclassReplayReferences` | moves with the set (21a puts it in a regclass file) |
| `pgIdentifier` (`deparse_renderer.go`) | `identifyAddress`, `identifyOwnedAddress` | pure (`parser`, `catalog`); 21a moves it to `catalog` |
| `rulesOf`, `ruleDefinition.Name`/`.OID` (rule payload) | `ownedAddressByName`, `ownedAddressName` | read rule identity through `catalog.RulesPayload` (5j) |

Imports of the set: `analyze` (`NamespaceSelection`, the lookup kinds,
`ViewLiteralPath`, `RegtypeConstant`, ...), `exec` (`FinalRows`,
`DetachedPresentation`, `OperatorGroups`, the output keys, `Context`),
`plan`, `session` (`Operation`, notice levels, `DatabaseName`),
`routine` (`ObjectRoutineKind`, `OperatorSeeded`, ...), `sequence`
(`Environment`), `pgcatalog` (`ClassKind`, collation versions),
`authorization`, `catalog`, `access`, `ids`, `parser`, `sqltype`,
`sqlstate`. All are below L11. Nothing in the set imports `stored`.

### Move set by file (21b)

"Moves" is clean lines; "stays" names what remains in the root.

| File | Lines | Moves | Stays in the root |
|---|---:|---:|---|
| `object_identity.go` | 446 | 431 | — |
| `regproc_persistence.go` | 268 | 249 | `scalarRenderer.recordRoutineReferences` (21a moves it to the renderer) |
| `domain_dump.go` | 316 | 221 | `domainDumpStatement`, `dumpDomain` (steps 18 and 15e) |
| `object_address_lookup.go` | 556 | 168 | `lookupObjectAddress`, `objectLookupDescriptor`, `objectLookupPath` |
| `regproc_output.go` | 190 | 121 | `StatementScope.FormatRegproc`, `FormatRegprocedure`, `presentation`, `presentationOrZero`, and the slot type `presentationCaches` (21a) |
| `regproc_lookup.go` | 125 | 117 | — |
| `regtype_output.go` | 128 | 110 | `StatementScope.FormatRegtype` |
| `aclitem_output.go` | 118 | 98 | `StatementScope.FormatACLRole` |
| `regoperator_persistence.go` | 105 | 92 | — |
| `sequence_output.go` | 181 | 88 | `Engine.captureRegclassOutput`, `StatementScope.FormatRegclass` |
| `regts_persistence.go` | 94 | 86 | — |
| `regoperator_output.go` | 120 | 83 | `StatementScope.FormatRegoper`, `FormatRegoperator`, `formatOperator` |
| `scalar_function_sql.go` | 232 | 83 | `scalarRenderer.scalarFunctionSQL` (step 22) |
| `regrole_output.go` | 91 | 71 | `StatementScope.FormatRegrole` |
| `regts_output.go` | 101 | 66 | `StatementScope.FormatRegconfig`, `FormatRegdictionary`, `formatRegTS` |
| `regtype_persistence.go` | 103 | 65 | `scalarRenderer.recordCatalogAliasDependencies`, `recordCatalogAliasReferences` (21a) |
| `regobjects_output.go` | 89 | 50 | `Engine.captureRegObjectRowsValueLocked`, `StatementScope.FormatRegcollation`, `FormatRegnamespace` |
| `aclitem_input.go` | 63 | 46 | `Engine.captureACLItemInput` |
| `aclitem_dump.go` | 150 | 39 | `preflightACLItemDump` (a function since 18a) |
| `domain_remap.go` | 215 | 39 | everything but `remapRegclassDatum` |
| `regclass_capture.go` | 141 | 36 | `Engine.captureRegclassInput`, `Engine.regclassLocked`; `capturedRegclassInput` (21c) |
| `visibility_metadata.go` | 45 | 35 | — |
| `regoperator_input.go` | 210 | 30 | `Engine.captureOperatorInput`; `capturedOperatorInput` (21c) |
| `acl_empty.go` | 31 | 25 | — |
| `regclass_dump.go` | 37 | 24 | — |
| `regrole_input.go` | 32 | 21 | — |
| `visibility_inquiry.go` | 127 | 15 | `Engine.captureVisibilitySnapshotLocked`, `statementHost.VisibilityInquiry` |
| `object_address.go` | 98 | 12 | `identifyObjectValues` and four `statementHost` methods |
| `regtype_analysis.go` | 112 | 10 | `Engine.regtypeAnalysisLocked`, `regtypeAnalysisValidLocked` |
| `regproc_input.go`, `regts_input.go`, `regtype_input.go`, `regobjects_input.go` | 621 | 0 | the `Engine.capture*` constructors; the resolver types (21c) |

`aclitem_remap.go` (`Engine.aclItemImporter`, Restore) and
`principal_remap.go` stay for step 30.

### The input resolvers (21c, about 0.7k lines)

`capturedRegclassInput`, `capturedRegObjectInput`, `capturedOperatorInput`,
`capturedRoutineInput`, `capturedRegTSInput` and `capturedRegtypeInput`
(55, 92, 152, 121, 75 and 215 lines) are dirty for three reasons only:

1. They hold an `AuthorizationView` (it holds `*Engine`) but use only
   `auth.Catalog`, `auth.Roles`, `auth.CanUse` and the wrappers
   `definitionLookup`, `definitionType`, `definitionNamespace`,
   `definitionDescriptor`, `definitionArray`. Each wrapper is one call to
   `catalog.DefinitionLookupAt`, `DefinitionTypeAt`, `DefinitionNamespaceAt`,
   `DescriptorAt` or `DefinitionArrayAt` with the view's catalog and its
   `definition` reader (`definition_reader.go`).
2. `capturedRegtypeInput.resolveTypeName` (`regtype_input.go:77`) finds
   its notice target at call time: `scopeOf(r.authority.Host)` and then
   `scope.op`. The target is per execution, not per capture.
3. `captureRegObjectsOnPath` builds a path view from
   `bindingAuthorityView` fields and `temporaryNamespace`. It is a
   constructor and stays in the root.

### Outside users

313 production uses outside the set, from almost every area (table 36
files, expression utilities 28, schema-level DDL 20, partitions 17,
typedef leftovers 16, statement code 15, indexes 13, ...), and 243 test
uses. The host methods that become forwarders:

- `analyze.Host`: `Regclass`, `CaptureElementInput`, `ResolveOperatorText`,
  `ResolveRegtypeText`, `CaptureTypeDiagnosticInput`,
  `CaptureTypeDiagnosticName` (bodies in `analysis_host.go`).
- `exec.Host`'s `PresentationFormatters` (the nine `Format*` methods in
  `statement_host.go`): they keep the statement caches and call the
  `regobj` spellings.
- `pgcatalog`'s producer input `TypeOutput`.

## Part A

### 21a — In place

One commit per item; `make build` and the targeted units after each.

1. **Renames.**
   - `sequence_output.go` → `regclass_output.go` (it is regclass output).
   - Move `catalogRegclassOutput` (with `FormatRegclass`) from
     `domain_dump.go` into `regclass_output.go`, and `remapRegclassDatum`
     from `domain_remap.go` into `regclass_dump.go`.
   - Move `catalogTypeDeclaration`, `catalogTypeDeclarationAt`,
     `catalogColumnDeclaration`, `catalogLiteralSQL`, `catalogLiteralWithACL`
     and `catalogLiteralWithACLAt` from `domain_dump.go`, and `literalSQL`
     from `scalar_function_sql.go`, into a new `catalog_literal.go`.
     `domain_dump.go` keeps only `domainDumpStatement` and `dumpDomain`.
   - Move `namedObjectVisible`, `namedObjectVisibleAt` (from
     `visibility_inquiry.go`) and the whole of `visibility_metadata.go`
     (`collationCandidate`, `selectCollation`, `visibleCollation`) into a
     new `namespace_visibility.go`. Delete `visibility_metadata.go`. These
     are search-path visibility rules, not MVCC visibility.
   - Move `objectAddressTextArray` (`object_address.go`) and the clean
     helpers of `object_address_lookup.go` (`objectInputArray`,
     `objectInputClass`, `objectListLength`, `ownedAddressByName`) into
     `object_identity.go`, so the identity code sits in one file.
   - Move `presentationCaches` and the `StatementScope.presentation` and
     `presentationOrZero` accessors out of `regproc_output.go` into a new
     root file `statement_presentation.go`. It is a scope slot, not reg*
     code.
2. **Renderer methods out of reg* files.** Go keeps methods with their
   type, and `scalarRenderer` goes to `stored` (step 22). Move
   `scalarRenderer.recordRoutineReferences` (`regproc_persistence.go`),
   `recordCatalogAliasDependencies` and `recordCatalogAliasReferences`
   (`regtype_persistence.go`) next to the renderer (in `default.go` for
   now; step 22a renames it). Do not change them.
3. **`pgIdentifier` goes to `catalog`.** It is pure: it quotes a name when
   `parser.IdentifierNeedsQuotes` says so. Add it to
   `catalog/diagnostic_names.go` next to `QuoteIdentifier` as
   `catalog.Identifier` and rewrite the 52 production uses in 16 files and
   the test uses. Typedef's dump code needs it too, so it cannot live in
   `regobj` or `stored`. If catalog 5i (item 72) already moved it, skip
   this item.
4. **Dead code.** Delete `routineContainerLiteral`
   (`regproc_persistence.go:118`); nothing calls it, tests included.
   Keep `routineContainerLiteralAt`. `walkOperatorReferences` and
   `walkRegTSReferences` are used only by tests; keep them (tests follow
   the code; they move with it).
5. **Re-measure.** Run `frontier` with `-decls` set to the 21b set (the
   list in `.cswd/tmp/ENGINE-SPLIT-POSTPONED-PLAN/regobj/set1.txt` plus
   `remapRegclassDatum`). OUT must list only `sqlError` and the rule
   payload edge. If anything else appears, stop and report it.

**Open questions.** None known. If 5j has not added a rule-identity
accessor by the time 21b starts, stop and ask; do not add one here.

### 21b — Create `regobj` and move the clean set

1. **Rule identity.** `ownedAddressByName` and `ownedAddressName` iterate
   `rulesOf(d)` for the OID and name of each rule (object class 2618).
   Rewrite both loops over the `catalog.RulesPayload` accessor that 5j adds
   (`d.Rules`), so the identity code names no root type. Check 5j's
   README note for the accessor's exact name.
2. **Names.** Export what the root still calls. Proposed names (settle in
   this sub-step and record them in the README):

   | Root name | `regobj` name |
   |---|---|
   | `regprocSpelling`, `routineSpelling`, `operatorSpelling`, `regtypeSpelling`, `regTSSpelling`, `regObjectSpelling`, `regclassSpelling(At)` | `ProcSpelling`, `RoutineSpelling`, `OperatorSpelling`, `TypeSpelling`, `TextSearchSpelling`, `ObjectSpelling`, `ClassSpelling(At)` |
   | `catalogRoutineOutput`, `catalogRegtypeOutput`, `catalogRegclassOutput` | `RoutineOutput`, `TypeOutput`, `ClassOutput` |
   | `dumpOperatorOutput`, `dumpRegTSOutput`, `dumpCatalogAliasOutput` | `DumpOperatorOutput`, `DumpTextSearchOutput`, `DumpAliasOutput` |
   | `operatorLiteralSQL`, `routineLiteralSQL`, `routineContainerLiteralAt` | `OperatorLiteral`, `RoutineLiteral`, `RoutineContainerLiteralAt` |
   | `walk*ReferencesAt`, `remap*Datum` | `Walk*ReferencesAt`, `Remap*Datum` |
   | `catalogTypeDeclaration(At)`, `catalogColumnDeclaration`, `catalogLiteral*`, `literalSQL` | `TypeDeclaration(At)`, `ColumnDeclaration`, `Literal*`, `ConstantLiteral` |
   | `selectRoutine`, `selectExactRoutine`, `routineIdentityByOID` | `SelectRoutine`, `SelectExactRoutine`, `RoutineIdentityByOID` |
   | `capturedACLItemInput`, `capturedACLItemOutput`, `aclItemContextFormatter`, `containsEmptyACL` | `ACLItemInput`, `ACLItemOutput`, `ACLItemContextFormatter`, `ContainsEmptyACL` |
   | `objectIdentity`, `identifyAddress`, `identifyOwnedAddress`, `objectInputClass`, ... | `ObjectIdentity`, `IdentifyAddress`, `IdentifyOwnedAddress`, `ObjectInputClass`, ... |

3. **Move.** Use the move driver (`mv.sh STAGE DEST`): collect the set into
   a staging file, check `frontier` OUT, export with `expspec` and `movex`,
   move with `movespec`, then `goimports`, `keylit` and `gofmt`. Whole
   files move with `git mv` (`object_identity.go`, `regproc_lookup.go`,
   `regoperator_persistence.go`, `regts_persistence.go`,
   `regclass_dump.go`, `regrole_input.go`, `acl_empty.go`,
   `namespace_visibility.go`, `catalog_literal.go`). Add
   `regobj/errors.go` with the package's `sqlError`.
4. **Aliases.** Leave type aliases (`type catalogRoutineOutput =
   regobj.RoutineOutput`, ...) in one root file `regobj_aliases.go` only if
   more than about 40 root files name the type; qualify functions and the
   other types directly. List the aliases in Status. Remove them before the
   step is done (in this session if few; otherwise as the last commit of
   21c).
5. **`routine.ObjectRoutineKind`.** Its only callers are the two identity
   files. Leave it in `routine` (`regobj` imports `routine`).
6. **Check layering.** `go list -f '{{.ImportPath}}: {{join .Imports " "}}'
   ./internal/engine/regobj` must not name `stored`, the root or an upper
   feature. Then confirm no lower package imports `regobj`.

### 21c — Move the input resolvers

1. **Authority value.** Add to `regobj`:

   ```go
   // Authority is the read authority a captured input resolver checks
   // names with: the statement's catalog and roles, its environment and
   // the sparse definition overlay of a cooking DDL statement.
   type Authority struct {
       View        access.View
       Environment access.Environment
       Definitions catalog.DefinitionReader // nil outside cooking DDL
   }
   ```

   Rewrite the resolvers to hold an `Authority` instead of an
   `AuthorizationView`: `auth.CanUse(ref)` becomes
   `a.View.CanUse(a.Environment, ref)`; `auth.definitionLookup(key)`
   becomes `catalog.DefinitionLookupAt(a.View.Catalog, a.Definitions, key)`;
   `definitionDescriptor` becomes `catalog.DescriptorAt`; `definitionType`,
   `definitionNamespace` and `definitionArray` become the matching
   `catalog.Definition*At`. The root constructors build the value as
   `regobj.Authority{View: auth.View, Environment: auth, Definitions:
   auth.definition}`. Do it in place first (one commit), then move.
2. **Notices (pattern 3).** Declare in `regobj`:

   ```go
   // NoticeHost is the statement capability a resolver emits type-name
   // parser notices through. The root statementHost implements it.
   type NoticeHost interface {
       TypeNameNotice(sqlstate.Error)
   }
   ```

   `resolveTypeName` keeps its order: with no `authority` it uses its
   captured `op`; with one it asserts `r.authority.Host.(regobj.NoticeHost)`
   at call time and emits nothing when the assertion fails (today: no
   scope). `statementHost.TypeNameNotice` emits at `session.LevelNotice`
   on `scope.op`. Add a compile-time assertion next to the other
   `statementHost` assertions. Keep the portal context suffix exactly.
3. **Move** the six resolver types and their methods. `captureRegObjectsOnPath`,
   `regtypeAnalysisLocked` and every `Engine.capture*Input` constructor
   stay in the root.
4. Remove the remaining aliases.

## Invariants

- Output, input, dump spellings and diagnostics stay byte-identical:
  seeded operators by OID and user operators by signature
  (`operatorLiteralSQL`), `routineLiteralSQL`, the numeric OIDs of the
  dump outputs, `preflightACLItemValue`, the identity strings and every
  `remap*Datum` result.
- Resolvers check privileges with the same authority and catalog view as
  today (`CanUse` with the statement environment; the cooking overlay
  only while DDL cooks). No resolver caches a permission decision.
- Notice order and text do not change; a notice goes to the operation of
  the current execution, never of the capture.
- Prepared reg* literals keep their analysis-identity rules (`AGENTS.md`
  prepared SQL bullets): this step moves code, not lifetimes.
- No new mutex. Nothing in `regobj` takes `Engine.mu`.

## Targeted tests

- Engine units: grep `*_test.go` for the moved names (`regprocSpelling`,
  `catalogRoutineOutput`, `operatorLiteralSQL`, `remapOperatorDatum`,
  `objectIdentity`, `capturedRegtypeInput`, `catalogTypeDeclaration`,
  `catalogLiteralSQL`, `pgIdentifier`, ...). About 240 test uses. Rewrite
  them mechanically; tests of pure moved code (spellings, literals,
  identity) move into `regobj`. Tests never keep code in the root.
- Reference selections (quick mode, then `REFERENCE=1`; names from
  `grep -rh '^func TestReference' reference`; never modify `reference/`
  or `smoke/`):
  `make check RUN='^TestReference(Reg|ACLItem|ObjectAddress|FormatRegclass|SequenceRegclass|ArrayRegclass|RLSPolicyRegtype|SQLCursorRegtype|UpdateReturningCapturedRegclass|PGDependRegclass|PartitionConstraintsRegclass)'`
  plus `RUN='DumpRoundTrip|NativeControls'` for the dump spellings.
  Some `REFERENCE=1` tests also fail on `master` (oracle drift): rerun a
  failure on a detached `master` worktree before reporting it, and report
  anything that passes there.
- `make bench` is not needed: seeding and startup do not change.

## Documentation

- Create `internal/engine/regobj/README.md` (21b): what it owns (the list
  in Goal), what stays in the root and why (statement caches, engine
  constructors, host forwarders), the byte-identity invariant, and
  `## Testing` with the selectors above.
- `AGENTS.md`: add `regobj` to the brace list of the engine subpackages
  bullet. The file must not grow.
- `internal/engine/README.md`: move any `## Testing` selectors of moved
  tests. Update `docs/` and package READMEs that name `sequence_output.go`,
  `visibility_metadata.go` or the moved functions (grep).
- Tick the sub-step here and in
  [ENGINE-SPLIT-STATUS.md](ENGINE-SPLIT-STATUS.md); update the step 21
  cell of ENGINE-SPLIT.md §8.

## Done when

`regobj` exists, imports nothing above `analyze` and `exec`, and no root
file declares reg* spelling, persistence, literal, type-declaration or
identity code; the resolvers live in `regobj` behind `regobj.Authority`;
no aliases remain; the checks in §8.3 pass.

## Status

- [ ] 21a — In place (item 64)
- [ ] 21b — `regobj` created and clean set moved (item 65)
- [ ] 21c — Input resolvers moved (item 66)

Outstanding aliases: none yet.

### Notes for later steps

- Step 22 (`stored`) imports `regobj` for the type-declaration, literal,
  dump-spelling and remap helpers; the `stored` fixed point uses only
  21b items, not the 21c resolvers.
- Step 18 (`dump`) declares `dump.Presenter`; its type and column
  declaration and literal methods can be implemented over `regobj`
  (`TypeDeclaration`, `ColumnDeclaration`, `Literal*`,
  `RoutineIdentityByOID`).
- `presentationCaches` stays a root scope slot; its maps are keyed by
  `exec` keys and filled through the `regobj` spellings.
