# internal/engine/regobj

Catalog objects and typed values as SQL text, and the way back: the
object-identifier types, type declarations, typed literals, `aclitem` I/O,
object identity and the catalog inquiries over them.

## Imports

Layer L11 of [ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture). It
imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `exec`, `expr`,
`functions`, `ids`, `pgcatalog`, `plan`, `routine`, `session`, `typedef`,
`parser`, `sqlstate`, `sqltype` and the standard library, never the engine,
`stored` or an upper feature. Only the upper features, `stored` and the engine
import it.

## What it owns

- **reg\* spellings and output** (`reg*_output.go`): `ClassSpelling`,
  `ProcSpelling`, `RoutineSpelling`, `TypeSpelling`, `OperatorSpelling`,
  `TextSearchSpelling`, `ObjectSpelling`, `WriteRoutineArguments`; the
  catalog-backed formatters `ClassOutput`, `RoutineOutput`, `TypeOutput`
  (`View`, `Path`) and `RoleOutput`; output capture into `exec.FinalRows`
  (`Capture{Class,Proc,Operator,TextSearch,Object,Role,ACLItem}OutputValue`);
  `RoleContextFormatter`. `CaptureRowsOutput` (`rows_output.go`) runs them
  over every flat, anonymous and selected-source value of a result, with the
  collation path as a callback read on the first non-NULL regcollation
  value. `SpellingCache` (`spelling_cache.go`) is a statement's regproc,
  regprocedure, operator and text-search spellings by OID, filled on first
  use; the caller passes the inquiry catalog and path on every call.
  Diagnostic `WriteRoutineArguments` accepts a transient definition reader so
  CREATE validation sees staged argument types without cloning a catalog.
- **Raw type inquiries** (`type_format.go`): `FormatType` renders canonical SQL
  names and raw typmods over a supplied catalog and type path, keeping absent
  modifiers distinct from explicit -1. This is not declaration validation or
  REGTYPE's missing-OID output. `BaseType` follows each immediate domain link
  through that same catalog, returning not-found for a missing link.
  Logical UNKNOWN cast naming authenticates every ordered no-op target against
  the current definition/catalog descriptor generation. This metadata-only
  check cannot grant a pseudo codec or turn an outer no-op into a second IO cast.
- **Prepared reg\* literal analysis** (`regtype_analysis.go`): `Analysis`,
  the cell a prepared statement holds for its reg\* constants (`Constants`,
  the role, database and lookup paths they were resolved under), with
  `NewAnalysis`, `Valid`, `ForReanalysis`, `Invalidate` and
  `RetireConfigurations`. They run over an `Observation`
  (the engine's catalog domain, the session's database name, the
  statement's `access.View` and its environment) and `LookupPaths`, the type
  and collation path callbacks, read only when the constants need them. The
  callbacks are a separate parameter so they do not escape with the
  environment.
  REGCONFIG occurrences retain their original immutable typed OID and operand
  provenance separately from current configuration/dictionary observations.
  Reanalysis projects only eligible configurations into a private input slice;
  other aliases retain their ordinary retirement policy. Eligibility requires
  the same domain, epoch, role, database, effective path and existing namespace
  generation, with current source-qualified namespace authority. Configuration
  rename, move, mapping edits or deletion do not retarget the original OID;
  no permission check is added on its current destination namespace. Real
  namespace publication retires source captures even if the effective path is
  unchanged. An absent original has no current dependencies and remains an
  actual typed value for the runtime's missing-object diagnostic.
- **reg\* input helpers** (`regclass_input.go`, `regrole_input.go`,
  `regoperator_groups.go`, `regproc_lookup.go`): `ClassNumeric`,
  `ClassQualifiedName`, `RoleInput`, `AccessRoleID` (the role argument of the
  ACL inquiry functions), `CollectOperatorGroups`, `VisibleOperators`,
  `SelectRoutine`, `SelectExactRoutine`, `RoutineIdentityByOID`,
  `RoutineDescriptorOwner`.
  Exact selection reads represented descriptors first. Its released fallback
  suppresses stale aliases when that identity's current name, namespace or
  signature has changed, then continues the normal namespace path. Genuinely
  absent release identities remain lookup-only, not executable descriptors.
- **Captured input resolvers** (`reg*_input.go`, `captured_input_target.go`,
  `input_authority.go`): `ClassInput`, `TypeInput`, `OperatorInput`,
  `RoutineInput`, `TextSearchInput` and `ObjectInput`, built by `New*Input` over
  an `InputCapture` (an `Authority`, the lookup path, and the session's
  database, token and TEMP namespace at capture). `Authority` is the
  statement's `access.View`, its `access.Environment` and the cooking DDL
  definition overlay (nil otherwise); resolvers check USAGE with
  `View.CanUse(Environment, ref)` and read names through the
  `catalog.Definition*At` readers. `TypeInput.ResolveParsedTypeName` serves
  already-parsed type syntax. The `Retarget*` methods implement the `analyze`
  retarget interfaces, and `DetachCursorInput` rebinds a cursor's resolvers to
  its execution authority.
  `TextSearchInput.ResolveTextParser` uses that same captured, TEMP-free path
  for parser names, with explicit namespace USAGE checked before lookup.
  Parser names remain TEXT inputs; no regparser type or parser-owner ACL is
  introduced. Both seeded and user parsers resolve current descriptor/name keys
  and retain actual dependency revisions when requested. Configurations capture
  their actual parser revision alongside dictionary observations.
  REGCONFIG and REGDICTIONARY resolve actual descriptor identities, capture
  operand revisions and retain visibility-sensitive output spellings. Their
  persistence walkers recurse through arrays, domains, composites and ranges;
  native replacement remaps actual configuration identities rather than names.
- **Dump spellings, stored-value walkers and remap** (`regclass_dump.go`,
  `reg*_persistence.go`): `DumpOperatorOutput`, `DumpTextSearchOutput`,
  `DumpAliasOutput`; `OperatorLiteral`, `RoutineLiteral`;
  `Walk{Routine,Operator,TextSearch}References(At)`, `ClassReplayReferences`;
  `Remap{Class,Routine,Operator,TextSearch,Alias}Datum`;
  `DomainInvalidDumpEvents` (`domain_invalid_dump.go`), the dump graph events of
  NOT VALID domain CHECKs over a `dump.Resolver` (here, not in `typedef`,
  because it walks `ClassReplayReferences` and `regobj` imports `typedef`).
- **Type declarations and literals** (`catalog_literal.go`): `TypeDeclaration(At)`,
  `ColumnDeclaration`, `Literal`, `LiteralWithACL(At)`, `ConstantLiteral`.
  Declaration rendering accepts canonical nominal-only domain/array metadata
  only with exact catalog-owned provider pointers and reciprocal companions.
  This does not admit those usages as executable codecs or stored values.
  Deep constructor literals reuse `float4_dump.go` for exact raw FLOAT4 and
  negative-circle leaves. Custom ranges and companions use genuine type-owned
  calls from `range_constructor_literal.go`, recursively rendering finite
  endpoints and normalized members through the same leaf renderer. Reference
  discovery, output capture and remap also visit these typed endpoints.
  `ConstructorArrayTargetBounds`
  and `ConstructorBoundedInputContext` preserve non-one-based array dimensions
  through ordinary INSERT targets without native-only sidecars. That authority
  addresses only the literal's root array (through domain wrappers); recursive
  ROW fields and array elements never inherit it. Nested rank-one constructor
  arrays retain their own bounds through closed qualified `array_fill`/`array_cat`
  SQL; non-one-based multidimensional nested arrays need their own bounded INSERT
  target. Captured BOX/CIRCLE/PATH/POINT/LINE/LSEG/POLYGON literals retain explicit
  owning-type annotations, including NULL. Stored query rendering uses captured
  source AST literals through `ConstantLiteral`, independently of expression
  providers' display/replay channels. Bare text could reselect a geometric
  overload or typename input shortcut, select `length(text)` instead of
  `length(lseg)`, or lose the `npoints(path)`/`npoints(polygon)` overload on replay.
- **Search-path visibility of named objects** (`namespace_visibility.go`,
  `visibility_inquiry.go`): `NamedObjectVisible`, `NamedObjectVisibleAt`,
  `CollationCandidate`,
  `SelectCollation`, `VisibleCollation`, and `VisibilityInquiry`, which answers
  the `pg_*_is_visible` and collation-version inquiries over the statement's
  visibility snapshot (catalog, relation path, TEMP-filtered path). These are
  name-visibility rules, not MVCC visibility.
  The explicit-reader variant uses the same exact descriptor name/signature key
  over unpublished definitions and is never retained in a formatting cache.
- **`aclitem` I/O** (`aclitem_*.go`, `acl_empty.go`): `ACLItemInput`,
  `ACLItemOutput`, `ACLItemValidationOutput`, `ACLItemContextFormatter`,
  `ACLItemOutputContext`, `PreflightACLItemValue`, `ContainsEmptyACL`.
- **Object identity** (`object_identity.go`): `pg_identify_object`,
  `pg_describe_object` and `pg_get_object_address` through
  `IdentifyAddress`/`ObjectIdentity`, and the input helpers `ObjectInputArray`,
  `ObjectInputClass`, `ObjectListLength`, `ObjectAddressTextArray`,
  `ObjectAttributeName`, `ObjectRelationKind`, `OwnedAddressByName`. Rule
  identity is read through `(*catalog.Relation).VisitRules`. Ordinary and
  persistent foreign-key triggers share the canonical owned-relation renderer;
  `catalog.ForeignKeyTriggerFacts` supplies RI names and actual trigger relations,
  including action triggers on the referenced table.
  Cast identity uses actual catalog descriptor endpoints, with fixed builtin
  metadata only as a fallback; qualified endpoint identities and path-sensitive
  descriptions share the canonical type renderer. Casts remain ownerless, with
  no schema/simple name or temporary status.
  Subscription descriptions use the raw stored name, independently of quoted SQL
  identity/name fields; address components retain the decoded database-local name.
  User mappings resolve their mapped principal through the supplied authorization
  snapshot and their server through the catalog descriptor. PUBLIC uses `public`;
  identities quote the role but retain the raw server name, while address arrays
  carry the raw role and server separately. Storage keys and diagnostic cached
  names never supply identity, schema or simple-name fields.
  `DroppedAddress` (`dropped_identity.go`) shares that renderer and owner lookup,
  capturing a plain immutable `catalog.DroppedObject` before deletion. Its
  nullable schema/simple names are raw identifiers, separate from inquiry
  spellings. The canonical identity APIs take the executing physical session
  token explicitly: only its non-toast TEMP namespace renders as `pg_temp`.
  Peer TEMP and own temporary-toast namespaces retain their physical names,
  including raw DDL schema metadata; detached readers pass the zero token.
  Temporary routine/operator identity text retains its physical namespace name
  even for the owner, independently of canonical schema/address components.
  Forward and inverse routine kinds read the exact represented `Procedure`
  descriptor first (aggregate/procedure, otherwise function). Only unrepresented
  identities use released kind facts; an aggregate helper's dependency owner
  never supplies the helper's kind.
  Rules retain NULL schema/name and false temporary status even when their
  rendered identity names a temporary owner. Deletion provenance remains the
  caller's responsibility.
- **Object address inquiries** (`object_address_lookup.go`,
  `identify_values.go`): `AddressLookup.Lookup` answers `pg_get_object_address`
  over the comment catalog and roles and an `InputCapture` of the deparse
  authority and reference path; two root capabilities complete it, `Namespace`
  (the function-lookup selection of a two-part name, called only after the
  schema is known to exist) and `Notice` (parser notices and type-modifier
  warnings, in order). `IdentifyValues` builds the `pg_identify_object`,
  `pg_identify_object_as_address` and `pg_describe_object` results.
  Access-method lookup/identity and family/class descriptions use the captured
  global descriptor authority, including quoted custom method names. Handler
  identity metadata is separate from executable native capability.
  Routine input arrays mean exact input signatures, including zero inputs, not
  bare-name overload selection. Raw name cells remain raw dotted diagnostic
  names; argument cells are parsed SQL types rendered canonically. Sequential
  syntax/NULL validation precedes the 100-input routine cap, which precedes
  semantic type and namespace lookup. Missing zero-input aggregates use `(*)`.
- **Comment inquiries** (`comment_inquiry.go`): `CommentText` answers
  `obj_description`, `col_description` and `shobj_description` over the
  comment catalog and roles; an empty text is NULL.

## Soft and hard input errors

Fresh-input recoverability is producer-owned: `sqlstate.SoftInputError` marks
only the bounded alias input's own name/missing/ambiguity/signature failures.
Type grammar and modifier helpers use `HardInputError`; ACL, cancellation,
structural-name and unrelated callback failures keep hard authority. Ordinary
STOP diagnostics, including missing-schema errors, are the same on both paths;
no SQLSTATE-family rule or cause-unwrapping decides recovery.

## Invariants

- Output, input and dump spellings, diagnostics and remap results are
  byte-stable because dumps and stored values replay them: seeded operators by
  OID and user operators by signature (`OperatorLiteral`), `RoutineLiteral`, the
  numeric OIDs of the dump outputs, `PreflightACLItemValue`, the identity
  strings and every `Remap*Datum` result.
- Every function reads only the `catalog.Read`, `authorization.Read`,
  definition reader and path its caller passes. Nothing here caches a
  permission decision or a catalog snapshot. A captured resolver keeps the
  authority its constructor captured; it never reads live session state.
- Type-name notices and type-modifier warnings go to the operation of the
  current execution, never of the capture: a resolver bound to an
  `exec.Authority` asserts `Host.(NoticeHost)` at each call and emits nothing
  when the host takes no notices; otherwise it emits to the captured operation.
- No mutex; nothing takes `Engine.Mu`. Callers that need the engine lock
  (`Engine.capture*`) hold it themselves, and the callbacks they pass
  (`CaptureRowsOutput`'s collation path, `LookupPaths`) run under it.

## What stays in the engine

- The statement caches and formatters: `presentationCaches` and the
  `StatementScope.Format*` methods behind `exec.Host`, keyed by `exec` keys and
  filled through the spellings above.
- The constructors that read engine state: `Engine.captureRegclassOutput`,
  `Engine.CaptureACLItemInput`, `Engine.captureRegObjectRowsValueLocked`,
  `Engine.regclassLocked`, the `Engine.capture*Input` methods and
  `captureRegObjectsOnPath` with the `inputCapture` helper (which drops the
  session and settings from the captured `AuthorizationView`), and the
  `analyze.Host` resolver forwarders.
- `statementHost.TypeNameNotice`, the `NoticeHost` implementation.
- The inquiry host adapters (`object_address.go`, `comment_inquiry.go`,
  `visibility_inquiry.go`): snapshot checks, `admitInquiryBody`, the
  `addressLookup` builder (its `Namespace` is `analyze.LookupNamespaces` over the
  deparse authority, which reads the engine's TEMP ownership and domain), the
  `Engine.Mu` `pg_import_system_collations` branch and
  `captureVisibilitySnapshotLocked`.
- `deparse_function.go`: only the missing-snapshot check and the
  `table.DeparseInputs` builder. The `pg_get_*def`, `pg_get_expr` and
  `pg_get_serial_sequence` bodies render in `table.Deparse`, because they use
  `stored` and `index`, which import `regobj`.
- `regtypeAnalysis` and `Engine.regtypeAnalysisLocked`/`ValidLocked`: the
  prepared-statement regtype analysis cell is statement state, not SQL text.
- `principal_remap.go` (the Restore stage and publication; see the
  [`control` README](../control/README.md)). The aclitem importer is
  `access.RoleStore.ACLItemImporter`, the Dump ACL preflight is
  `control.PreflightACLItems` and the scalar renderer is
  `stored.ScalarRenderer`.

## Testing

Package units (captured-input retargeting, notice routing, address lookup cuts,
dropped identity projection, collation selection, CBOX and path-measurement
literal transport) run without an engine. Unit tests that need an `Engine` live
in `internal/engine/core`
(`TestObjectAddress*`, `TestCommentInquiry*`, `TestACLItem*`) and
`internal/engine` (`TestVisibilityInquiry*`, `TestRegTS*`, `TestRegOperator*`,
`TestRegrole*`, `TestRegclass*`, `TestRegObjectAlias*`). Public scenarios live in
[reference/type_resolution](../../../reference/type_resolution/README.md#testing)
(reg\* types), [reference/object_address](../../../reference/object_address/README.md#testing)
and [reference/types](../../../reference/types/README.md#testing) (`aclitem`).
Input-origin changes must keep the direct, parameter and runtime presentation
groups of every reg\* family passing. Add `REFERENCE=1` for the oracle.
Raw type formatting and immediate-base traversal units live in `enginetests`
and use `RUN='^TestTypeDescribe(Formatter|BaseType)$'`; shared public scenarios
live in `reference/domain_types`.

```sh
make check RUN='^Test(CapturedInputs|RoutineInputParameter|TypeInputNotices|AddressLookup|DroppedIdentity|VisibilityInquiryCollation|GeometryConversionCBox|GeometryRelationLiteral)'
make check RUN='^TestAddressLookupRoutine(KindsAndRetainedAuthority|ReleasedAndHelperFallback|ArgumentCapPrecedence|SyntaxNullBeforeCap)$'
make check RACE=1 RUN='^Test(AddressLookup|DroppedIdentity|ObjectAddress)'
make check RUN='^TestGeometryMeasurePathLiteralTransport$'
make check RUN='^TestAnalysis(ConfigProjection|ConfigEnvironmentRetirement|PathsOnDemand)$'
make check RUN='^TestReferenceReg(class|type|role|Proc|Operator|TS|Collation|Namespace|Object)'
make check RUN='^TestReference(ObjectAddress|ACLItem)'
```
