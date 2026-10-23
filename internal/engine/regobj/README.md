# internal/engine/regobj

Catalog objects and typed values as SQL text, and the way back: the
object-identifier types, type declarations, typed literals, `aclitem`
I/O, object identity and the catalog inquiries over them. Step 21 of the
engine split created it (21b moved the clean set; 21c moved the captured
input resolvers; 21d moved the object address, comment and visibility
inquiries; 21e moved the result output capture, the spelling cache, the
prepared regtype analysis cell and the large-object ACL decode). The
package sits at
layer L11 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `access`, `analyze`, `authorization`, `catalog`, `dump`, `exec`,
`expr`, `functions`, `ids`, `pgcatalog`, `plan`, `routine`, `sequence`, `session`, `typedef`, `parser`,
`sqlstate`, `sqltype` and the standard library, and never the engine, `stored` or an upper
feature. Only the upper features, `stored` and the engine import it; never
`analyze`, `exec`, `session`, `pgcatalog` or a lower feature. See
[ENGINE-SPLIT-regobj.md](../../../docs/ENGINE-SPLIT-regobj.md).

## What it owns

- **reg\* spellings and output** (`regclass_output.go`, `regproc_output.go`,
  `regtype_output.go`, `regoperator_output.go`, `regts_output.go`,
  `regobjects_output.go`, `regrole_output.go`): `ClassSpelling`,
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
- **Prepared reg\* literal analysis** (`regtype_analysis.go`): `Analysis`,
  the cell a prepared statement holds for its reg\* constants (`Constants`,
  the role, database and lookup paths they were resolved under), with
  `NewAnalysis`, `Valid` and `Invalidate`. Both run over an `Observation`
  (the engine's catalog domain, the session's database name, the
  statement's `access.View` and its environment) and `LookupPaths`, the type
  and collation path callbacks, read only when the constants need them. The
  callbacks are a separate parameter so they do not escape with the
  environment.
- **reg\* input helpers** (`regclass_input.go`, `regrole_input.go`,
  `regoperator_groups.go`, `regproc_lookup.go`): `ClassNumeric`,
  `ClassQualifiedName`, `RoleInput`, `AccessRoleID` (the role argument of
  the ACL inquiry functions, B7), `CollectOperatorGroups`,
  `VisibleOperators`, `SelectRoutine`, `SelectExactRoutine`,
  `RoutineIdentityByOID`, `RoutineDescriptorOwner`.
- **Captured input resolvers** (`regclass_input.go`, `regtype_input.go`,
  `regoperator_input.go`, `regproc_input.go`, `regts_input.go`,
  `regobjects_input.go`, `captured_input_target.go`, `input_authority.go`):
  `ClassInput`, `TypeInput`, `OperatorInput`, `RoutineInput`,
  `TextSearchInput` and `ObjectInput`, built by `New*Input` over an
  `InputCapture` (an `Authority`, the lookup path, and the session's
  database, token and TEMP namespace at capture). `Authority` is the
  statement's `access.View`, its `access.Environment` and the cooking DDL
  definition overlay (nil otherwise); resolvers check USAGE with
  `View.CanUse(Environment, ref)` and read names through the
  `catalog.Definition*At` readers. `TypeInput.ResolveParsedTypeName` serves
  already-parsed type syntax. The `Retarget*` methods implement the
  `analyze` retarget interfaces, and `DetachCursorInput` rebinds a cursor's
  resolvers to its execution authority.
  Fresh-input recoverability is producer-owned: `sqlstate.SoftInputError` marks
  only the bounded alias input's own name/missing/ambiguity/signature failures.
  Type grammar and modifier helpers use `HardInputError`; ACL, cancellation,
  structural-name and unrelated callback failures retain their hard authority.
  Ordinary STOP diagnostics remain unchanged, including missing-schema errors;
  no SQLSTATE-family rule or cause-unwrapping decides recovery.
- **Dump spellings, stored-value walkers and remap**
  (`regclass_dump.go`, `regproc_persistence.go`,
  `regoperator_persistence.go`, `regts_persistence.go`,
  `regtype_persistence.go`): `DumpOperatorOutput`, `DumpTextSearchOutput`,
  `DumpAliasOutput`; `OperatorLiteral`, `RoutineLiteral`;
  `Walk{Routine,Operator,TextSearch}References(At)`,
  `ClassReplayReferences`; `Remap{Class,Routine,Operator,TextSearch,Alias}Datum`;
  `DomainInvalidDumpEvents` (`domain_invalid_dump.go`), the dump graph
  events of NOT VALID domain CHECKs over a `dump.Resolver` (here, not in
  `typedef`, because it walks `ClassReplayReferences` and `regobj` imports
  `typedef`).
- **Type declarations and literals** (`catalog_literal.go`):
  Deep constructor literals reuse `float4_dump.go` for exact raw FLOAT4 and
  negative-circle leaves. Actual values gate recursive typed ROW/ARRAY SQL;
  ordinary canonical values retain text literals. `ConstructorArrayTargetBounds`
  and `ConstructorBoundedInputContext` preserve non-one-based array dimensions
  through ordinary INSERT targets without native-only circle sidecars. That
  authority addresses only the literal's root array (through domain wrappers);
  recursive ROW fields and array elements never inherit it. Nested rank-one
  constructor arrays retain their own bounds through closed qualified
  `array_fill`/`array_cat` SQL. Non-one-based multidimensional constructor arrays
  still require their own bounded INSERT target; nesting cannot borrow one.
  `TypeDeclaration(At)`, `ColumnDeclaration`, `Literal`,
  `LiteralWithACL(At)`, `ConstantLiteral`.
  Captured BOX literals retain explicit BOX annotations, like CIRCLE/PATH;
  a quoted display alone could reselect a typename input shortcut on replay.
- **Search-path visibility of named objects** (`namespace_visibility.go`,
  `visibility_inquiry.go`): `NamedObjectVisible`, `CollationCandidate`,
  `SelectCollation`, `VisibleCollation`, and `VisibilityInquiry`, which
  answers the `pg_*_is_visible` and collation-version inquiries over the
  statement's visibility snapshot (catalog, relation path, TEMP-filtered
  path). These are name-visibility rules, not MVCC visibility.
- **`aclitem` I/O** (`aclitem_input.go`, `aclitem_output.go`,
  `aclitem_dump.go`, `acl_empty.go`, `aclitem_largeobject.go`):
  `ACLItemInput`, `ACLItemOutput`, `ACLItemValidationOutput`,
  `ACLItemContextFormatter`, `ACLItemOutputContext`,
  `PreflightACLItemValue`, `ContainsEmptyACL`, and `LargeObjectACL`, the
  decode of a Restore large-object ACL repair (canonical `aclitem[]`, SELECT
  and UPDATE only, principals of the output owner).
- **Object identity** (`object_identity.go`): `pg_identify_object`,
  `pg_describe_object` and `pg_get_object_address` through
  `IdentifyAddress`/`ObjectIdentity`, and the input helpers
  `ObjectInputArray`, `ObjectInputClass`, `ObjectListLength`,
  `ObjectAddressTextArray`, `ObjectAttributeName`, `ObjectRelationKind`,
  `OwnedAddressByName`. Rule identity is read through
  `(*catalog.Relation).VisitRules`.
- **Object address inquiries** (`object_address_lookup.go`,
  `identify_values.go`): `AddressLookup.Lookup` answers
  `pg_get_object_address` over the comment catalog and roles and an
  `InputCapture` of the deparse authority and reference path; two root
  capabilities complete it, `Namespace` (the function-lookup selection of a
  two-part name, called only after the schema is known to exist) and
  `Notice` (parser notices and type-modifier warnings, in order).
  `IdentifyValues` builds the `pg_identify_object`,
  `pg_identify_object_as_address` and `pg_describe_object` results.
  Access-method lookup/identity and family/class descriptions use the captured
  global descriptor authority, including quoted custom method names. Handler
  identity metadata remains separate from executable native capability.
- **Comment inquiries** (`comment_inquiry.go`): `CommentText` answers
  `obj_description`, `col_description` and `shobj_description` over the
  comment catalog and roles; an empty text is NULL.

Names settled in 21b: the engine's `regclass*`, `regproc*`, `regtype*`,
`regTS*`, `regObject*` and `regrole*` prefixes became `Class`, `Proc`,
`Type`, `TextSearch`, `Object` and `Role`; `catalog*` prefixes were
dropped (`catalogTypeDeclaration` is `TypeDeclaration`, `catalogLiteralSQL`
is `Literal`, `catalogRegclassOutput` is `ClassOutput`); `captured*` types
lost the prefix (`capturedACLItemOutput` is `ACLItemOutput`); `*LiteralSQL`
became `*Literal` and `literalSQL` is `ConstantLiteral`;
`remapCatalogAliasDatum`/`dumpCatalogAliasOutput` are
`RemapAliasDatum`/`DumpAliasOutput`. Helpers only the package uses keep
their lower-case names.

## Invariants

- Output, input, dump spellings and diagnostics are byte-identical to the
  engine code they came from: seeded operators by OID and user operators by
  signature (`OperatorLiteral`), `RoutineLiteral`, the numeric OIDs of the
  dump outputs, `PreflightACLItemValue`, the identity strings and every
  `Remap*Datum` result.
- Every function reads only the `catalog.Read`, `authorization.Read`,
  definition reader and path its caller passes. Nothing here caches a
  permission decision or a catalog snapshot. A captured resolver keeps the
  authority its constructor captured; it never reads live session state.
- Type-name notices and type-modifier warnings go to the operation of the
  current execution, never of the capture: a resolver bound to an
  `exec.Authority` asserts `Host.(NoticeHost)` at each call and emits
  nothing when the host takes no notices; otherwise it emits to the
  captured operation. Notice order, text and portal context are unchanged.
- No mutex; nothing takes `Engine.mu`. Callers that need the engine lock
  (`Engine.capture*`) hold it themselves, and the callbacks they pass
  (`CaptureRowsOutput`'s collation path, `LookupPaths`) run under it.

## What stays in the engine

- The statement slot and formatters: `presentationCaches`, which holds the
  `SpellingCache` beside the conversion defaults and the regex executor, and
  the `StatementScope.Format*` methods behind `exec.Host`, forwarders with
  the detached fallback.
- The constructors that read engine state: `Engine.captureRegclassOutput`
  (the `Engine.mu` window, the authority and the reference path around
  `CaptureRowsOutput`), `Engine.captureACLItemInput`,
  `Engine.regclassLocked` (`regclass_lookup.go`), the `Engine.capture*Input`
  methods and `captureRegObjectsOnPath` with the `inputCapture` helper
  (`captured_input.go`, which drops the session and settings from the
  captured `AuthorizationView`), and the `analyze.Host` resolver methods
  (forwarders).
- `statementHost.TypeNameNotice`, the `NoticeHost` implementation.
- The inquiry host adapters (`object_address.go`, `comment_inquiry.go`,
  `visibility_inquiry.go`): the snapshot checks, `admitInquiryBody`, the
  `addressLookup` builder (its `Namespace` is
  `lookupNamespacesSnapshot` (`analyze.LookupNamespaces`) over the deparse authority, which reads
  the engine's TEMP ownership and domain), the `Engine.mu`
  `pg_import_system_collations` branch and `captureVisibilitySnapshotLocked`.
- `deparse_function.go`: only the missing-snapshot check and the
  `table.DeparseInputs` builder. The `pg_get_*def`, `pg_get_expr` and
  `pg_get_serial_sequence` bodies render through `stored` and `index`,
  which import `regobj`, so table 24j moved them to `table.Deparse` (the
  21d decision).
- `Engine.regtypeObservationLocked` and the guarded
  `regtypeAnalysisLocked`/`regtypeAnalysisValidLocked` wrappers
  (`regtype_analysis.go`): the `Observation` and the sequence-environment
  and collation-lookup closures under `Engine.mu`. A statement without
  reg\* literals builds no observation.
- `Engine.restoreLargeObjectACL` (`largeobject_restore.go`): the repair
  parse, the `Engine.mu` window, the descriptor lookup, `ValidateACL`, the
  revision, the stage and the publication around `LargeObjectACL`.
- `StatementScope.captureSQLCursorOutput` (`sql_cursor_hold.go`): held-cursor
  FETCH output keeps its own capture loop (roles first, an eager collation
  path, the cursor's catalog), so it does not use `CaptureRowsOutput`.
- `aclitem_remap.go` and `principal_remap.go` (the Restore stage and
  publication); they stay in the root after step 30 (see the
  [`control` README](../control/README.md)). The Dump ACL preflight is
  `control.PreflightACLItems` since control 30d. The scalar renderer and its
  `record*` methods are `stored.ScalarRenderer` since 22b.

## Testing

```sh
make check RUN='^TestVisibilityInquiryCollationSelection$'
make check RUN='^(TestCapturedInputsRetargetOnlyTheirSinks|TestRoutineInputParameterAndLiteralCopies|TestTypeInputNoticesFollowTheCurrentHost|TestAddressLookupNamespaceAndNoticeCuts)$'
make check RUN='^(TestCaptureRowsOutputCollationPathOnDemand|TestAnalysisPathsOnDemand)$'
make check RUN='^TestGeometryConversionCBoxLiteralTransport$'
make check RUN='^TestReference(Reg|ACLItem|ObjectAddress|FormatRegclass|SequenceRegclass|ArrayRegclass|RLSPolicyRegtype|SQLCursorRegtype|UpdateReturningCapturedRegclass|PGDependRegclass|PartitionConstraintsRegclass)'
make check RUN='DumpRoundTrip|NativeControls'
make check RUN='^TestReferenceCastDefinitionNestedConstructorReplay$'
make check RUN='^TestMaterializedViewSnapshot(NestedConstructors|ConstructorProviders)$'
make check RUN='^TestReference(ObjectAddress|Comment|CollationVersion|VisibilityInquiry|RegCollationVisibility|CheckReviewScalarComment|DeparseConstraintsReviewProviderVisibility)'
make check RUN='^TestReferenceLargeObject(Restore|Privileges)$'
```

Add `REFERENCE=1` for the real PostgreSQL oracle. The engine unit tests of
this code need an `Engine` and stay in `internal/engine`
(`TestObjectAddress*`, `TestCommentInquiry*`, `TestVisibilityInquiry*`,
`TestRegTS*`, `TestRegOperator*`, `TestRoutine*`
in `regproc_test.go`, `TestRegrole*`, `TestRegclass*`, `TestACLValues*`,
`TestACLItem*`, `TestRegObjectAlias*`, `TestMaterializedRefreshOwnerContext`,
`TestConversionRegprocCapturedProcedurePresentation`, and the prepared
cell tests `TestRegOperatorPrepared*`, `TestRoutineRetainedWriteInputAuthority`,
`TestRoutineMixedOperatorCompoundCapture`, `TestPreparedRebind*`,
`TestDiscard*`).

Input-origin/caller preservation uses these bounded public groups, each both
as written and with `REFERENCE=1` (external900-second deadline):

```sh
make check RUN='^TestReferenceRegclass(Diagnostics|Authorization|Constructor|WriteDiagnosticPresentation|ContextualConstructorParameters)$'
make check RUN='^TestReferenceRegtype(Input|Diagnostics|InvalidSQLAndDemand|AliasModifierSyntax|HeldFrames)$'
make check RUN='^TestReferenceRegProc(Diagnostics|SignatureTypeSyntax|ValuesMetadata|Signatures|PreparedAuthority)$'
make check RUN='^TestReferenceRegOperator(Diagnostics|ParserEdges|NamespaceAuthorization|Signatures|PreparedAnalysis)$'
make check RUN='^TestReference(RegCollationInput|RegNamespaceInput|RegObjectBoundaryStringCasts|RegObjectBoundaryByteParameters|RegCollationVisibility)$'
make check RUN='^TestReferenceRegrole(Diagnostics|Parameters|AtomicityCancellation)$'
make check RUN='^TestReferenceRegTS(Diagnostics|NamespaceAuthorization|ValuesMetadata|PreparedLifetime)$'
make check RUN='^TestReferenceSequenceRegclass(InputSyntaxDiagnostics|ParameterInput|ParameterPresentationAndDemand|ParameterNamespaceInput|SourceInputStream)$'
make check RUN='^TestReference(ObjectAddressDiagnostics|ObjectAddressDiagnosticParameters|ObjectAddressTypeArgumentOrder|FDWImport|FDWImportAtomicity)$'
```

These exercise direct literal/parameter/runtime presentation, parsed signature
callbacks, qualified absence versus ACL, nested aliases and ordinary STOP
diagnostics. COPY-alias and all JSONB-record selected groups remain separately
required; these ordinary-consumer groups do not substitute for them.
