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
| `domain_dump.go` | 316 | 221 | `domainDumpStatement` (15e); `dumpDomain` is `dump.Statement` (18b) |
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
     `domain_dump.go` keeps only `domainDumpStatement` (`dumpDomain` is
     `dump.Statement` since 18b).
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
   (`regtype_persistence.go`) next to the renderer in `scalar_renderer.go`
   (22a split it out of `default.go`). Do not change them.
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

1. **Rule identity.** Done by 5j: `ownedAddressByName` and
   `ownedAddressName` read each rule's OID and name (object class 2618)
   through `(*catalog.Relation).VisitRules` (`catalog.RuleFact`), so the
   identity code names no root rule type. Re-run `frontier` to confirm the
   rule payload edge is gone from OUT.
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

### 21d — Object address inquiries (linear item 128)

Planned by the end-state review (linear item 114, ENGINE-SPLIT.md §2.8 and
§6 "Third round"), measured at `17e01ca8b`. Needs —.

`object_address_lookup.go` (373 lines) and `identifyObjectValues`
(`object_address.go:32`) are dirty only through `scopeOf`, the temporary
namespace, `lookupNamespacesSnapshot` and
`AuthorizationView.definitionLookup`. They run over the existing
`regobj.InputCapture` and `regobj.Authority.Definitions`, plus a
namespace-lookup and a notice function (cut tested). `comment_inquiry.go`
and the non-collation body of `VisibilityInquiry`
(`visibility_inquiry.go:47-88`) use the same capture. Measure
`deparse_function.go` (it needs `index.DefinitionSQL`, so it may go to
`index` instead) and record the decision.

Verify with `make build`, the targeted tests below plus the unit tests of
the touched files, the quick and `REFERENCE=1` selections for the area, and
quickcheck; no behavior, error text or error-order change, no aliases.

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

- [x] 21a — In place (item 64) (`faabae6f1`, `fdea22a7e`, `bc4972dc4`,
  `4c5e82e76`, `c8c5d6f0c`, `108381a52`, `3a26d7fbb`, `46a317e4d`, then the
  docs commit) · selected unit and dual-mode proof and quickcheck passed;
  racecheck/finalcheck skipped at user request
  - Re-measured at `351822746` (rebased onto `05ab40a48`): every 21a
    declaration was where the inventory says; `pgIdentifier` had 46
    production uses in 16 files (not 52) and no test uses. Catalog 5i had
    not run, so 21a moved it
    (`catalog.Identifier`, pinned by `TestIdentifierQuotesOnlyWhenNeeded`).
  - New root files: `regclass_output.go` (was `sequence_output.go`),
    `catalog_literal.go`, `namespace_visibility.go` (was
    `visibility_metadata.go`), `statement_presentation.go`. The three
    `scalarRenderer` recorders now sit after `scalarRenderer.record` in
    `scalar_renderer.go` (stored 22a split it out of `default.go`).
  - `frontier` over `set1.txt` plus `remapRegclassDatum` (output in
    `.cswd/tmp/PG18-ENGINE-SPLIT-REGOBJ-IN-PLACE/frontier-21a.txt`): OUT is
    `sqlError` and the rule payload edge (`rulesOf`, `ruleDefinition.Name`,
    `ruleDefinition.OID`) only.
  - No engine test moved or changed; `sequence_output_test.go` tests
    sequence prepared behavior, not regclass output, so it keeps its name.
- [x] 21b — `regobj` created and clean set moved (item 65) (`91fc9de2f`,
  `8ea91a2eb`, `ca7c8d575`, then the docs commit) · selected unit and
  dual-mode proof and quickcheck passed; racecheck/finalcheck skipped at user
  request
  - Re-measured at `205d59485` with `frontier -decls` over `set1.txt`
    (without the deleted `routineContainerLiteral`, plus
    `remapRegclassDatum`): 112 members; OUT is `sqlError` only, so the rule
    payload edge is gone (5j, step 1 confirmed). Started at `205d59485`;
    replayed onto `f51e6d8f9` (ddl 20b), `8cf863131` (copyfmt 19b,
    dump 18c), `92d17ca19` (exec 11f) and `e13730bdb` (ddl 20c, analyze
    12f, session 13f) with
    `.cswd/tmp/PG18-ENGINE-SPLIT-REGOBJ-PACKAGE/replay.sh`, which gave the
    same specs and diff.
  - `91fc9de2f` (split): `movedecl` put the set members of twelve mixed
    files into root `regobj_*.go` staging files, unchanged. Two files the
    inventory lists as mixed, `regproc_persistence.go` and
    `regtype_persistence.go`, were whole after 21a.
  - `8ea91a2eb` (export): `expspec` plus `movex` member mode exported the 87
    functions and fields the root uses, with the settled names (table in
    `internal/engine/regobj/README.md`). `collationCandidate`'s fields were
    added by hand: `collation.go` and `regobjects_input.go` build it with
    unkeyed literals, which `frontier` does not report.
  - `ca7c8d575` (move): 11 whole files and 12 staging files (2.8k lines)
    moved with `git mv`; `movex` qualify mode renamed the types (for example
    `capturedACLItemOutput` → `ACLItemOutput`, `catalogRegclassOutput` →
    `ClassOutput`, `objectIdentity` → `ObjectIdentity`) and qualified 101
    root files; `keylit` keyed 16 `CollationCandidate` literals; `regobj`
    got its own `sqlError` (`errors.go`). Set-internal helpers keep their
    lower-case names. `regobj` imports `analyze`, `authorization`,
    `catalog`, `exec`, `expr`, `ids`, `pgcatalog`, `plan`, `routine`,
    `session`, `parser`, `sqlstate` and `sqltype` (not `sequence` or
    `access`, which the inventory expected); only the root imports it.
    `routine.ObjectRoutineKind` stays in `routine`.
  - Deviation: `regtypeAnalysis` stays in the root. It is the prepared
    statement's regtype analysis cell, built and read only by
    `Engine.regtypeAnalysisLocked`/`regtypeAnalysisValidLocked`,
    `execute.go` and `expected_inputs.go`; no set member uses it. Moving it
    would only export eight state fields.
  - Aliases: none. The widest type, `ACLItemOutput`, is named by about 33
    root files (under the 40-file threshold).
  - Tests: `TestVisibilityInquiryCollationSelection` moved to
    `regobj/namespace_visibility_test.go`, its catalog state fixture
    inlined (`catalog.NewState`, `SetDescriptor`, `BindName`); the name is
    kept so the `^TestVisibilityInquiry` selector still matches it. The
    other 48 engine tests that use moved names build an `Engine` and were
    only qualified. None deleted.
  - Verification: build and vet (only the known `composites`, `copylocks`
    and `lostcancel` findings, the `RoutineIdentity` ones now reported under
    `regobj/`) after every commit; the 49 targeted unit tests; the two
    reference selections under Targeted tests in quick mode and with
    `REFERENCE=1` (on the `8cf863131` replay); `make quickcheck` on the replayed branch. `make bench`
    not needed.
- [x] 21c — Input resolvers moved (item 66) (`8565a61e7`, `fc8a45cdd`, `b65da8100`, then the docs
  commit) · selected unit/`RACE=1` and dual-mode proof and quickcheck
  passed; racecheck/finalcheck skipped at user request
  - Started at `c02c9d97e`; rebased onto `92ce8c794` (ddl 20d, partition
    25a) and `bfeb5f38a` (index 23a) before the docs commit. Re-measured:
    the six resolver types were where the inventory says, but more root
    code reached into them than it lists: `captured_input_target.go` (the retarget methods),
    identical cursor detach blocks in `protocol_portal.go` and
    `sql_cursor.go`, field pokes in `analysis_host.go`
    (`ResolveOperatorText`, `ResolveRegtypeText`), a literal in
    `object_address_lookup.go`, and `resolveParsedTypeName` callers in
    `routine_host.go`, `role_grant_routines.go` and
    `object_address_lookup.go`.
  - `8565a61e7` (authority, in place): `regobj.Authority`, `InputCapture`
    and `NoticeHost` (`regobj/input_authority.go`). The resolvers hold an
    `InputCapture` and call `View.CanUse(Environment, ref)` and the
    `catalog.Definition*At` readers. The constructors moved into the root
    `captured_input.go`; `inputCapture` reads the database, token and
    TEMP namespace and then drops the session and settings, as before, so
    the environment is the same stripped `AuthorizationView`.
    `statementHost.TypeNameNotice` implements `NoticeHost`. The seam test
    `TestTypeInputNoticesFollowTheCurrentHost` was written first and
    failed (no notice reached the host).
  - `fc8a45cdd` (call sites, in place): the analysis host retargets its
    fresh captures (`RetargetOperator`/`RetargetRegtype`) instead of
    setting fields; one `detachCursorInput` replaces the two cursor
    blocks; `regclass_capture.go` became `regclass_lookup.go` (it keeps
    `Engine.regclassLocked`). `frontier` over the move set (output in
    `.cswd/tmp/PG18-ENGINE-SPLIT-REGOBJ-RESOLVERS/frontier-21c.txt`): OUT
    is `sqlError` only.
  - `b65da8100` (move): five files and `captured_input_target.go` moved
    with `git mv`; `ClassInput` joined `regobj/regclass_input.go`. Names:
    `capturedRegclassInput` → `ClassInput`, `capturedRegtypeInput` →
    `TypeInput`, `capturedOperatorInput` → `OperatorInput`,
    `capturedRoutineInput` → `RoutineInput`, `capturedRegTSInput` →
    `TextSearchInput`, `capturedRegObjectInput` → `ObjectInput`;
    `resolveParsedTypeName` → `ResolveParsedTypeName`,
    `detachCursorInput` → `DetachCursorInput`. New `New*Input`
    constructors and the `TypeInput.Authority`/`Path` readers (used by
    `TestMaterializedRefreshOwnerContext`). `regobj` now also imports
    `access` and `typedef` (L9). The `definition*` wrappers on
    `AuthorizationView` stay; each still has other root users.
  - Deviation: `NoticeHost.TypeNameNotice` takes the notice level as well
    as the diagnostic. The type-modifier WARNING
    (`TestReferenceSQLCursorRegtypeWarnings`) also goes to the current
    execution's operation, not only the parser NOTICE. A resolver with an
    execution authority asserts `Host.(NoticeHost)` at each call; a
    typed-nil `*statementHost` or a nil `op` emits nothing, as `scopeOf`
    did.
  - Tests: `captured_input_target_test.go` and the new
    `regtype_input_notice_test.go` moved into `regobj`. The portal and
    position copy checks of `TestRoutineDiagnosticOwnership` became
    `regobj.TestRoutineInputParameterAndLiteralCopies`; the diagnostic
    half stays in the engine. Adapted only:
    `TestRegObjectAliasCapturedAuthority` (constructor),
    `TestRegTSProviderResolution` (builds its input from an
    `InputCapture` with the overridden path and recaptures the copy's
    view), and `TestMaterializedRefreshOwnerContext` (readers; it reads
    `materializedPopulation` through the environment). None deleted.
  - Verification: build and vet (only the known `composites`,
    `copylocks` and `lostcancel` findings) after every commit; the
    targeted units, with `-race` on the notice, cursor and materialized
    refresh tests; the reference selections under Targeted tests in quick
    mode and with `REFERENCE=1`; `make quickcheck` on the rebased branch.
    `make bench` not needed.
- [ ] 21d — Object address inquiries moved (linear item 128)

Outstanding aliases: none (21b and 21c added none).

### Notes for later steps

- 21c: the root resolvers already call the moved helpers
  (`regobj.CollectOperatorGroups`, `VisibleOperators`, `ClassNumeric`,
  `ClassQualifiedName`, `SelectRoutine`, `SelectCollation`,
  `CollationCandidate`), so moving them adds no new `regobj` exports for
  those. `regclass_capture.go`, `regoperator_input.go` and the four
  `*_input.go` files with the resolver types are the 21c move set.
- `frontier` does not count fields set by unkeyed composite literals as
  outside uses; check unkeyed literals of moving types by hand before the
  export commit.
- Step 22 (`stored`) imports `regobj` for the type-declaration, literal,
  dump-spelling and remap helpers; the `stored` fixed point uses only
  21b items, not the 21c resolvers.
- Step 18 (`dump`) declares `dump.Presenter`; its type and column
  declaration and literal methods can be implemented over `regobj`
  (`TypeDeclaration`, `ColumnDeclaration`, `Literal*`,
  `RoutineIdentityByOID`).
- `presentationCaches` stays a root scope slot; its maps are keyed by
  `exec` keys and filled through the `regobj` spellings.
