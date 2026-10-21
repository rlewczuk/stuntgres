# internal/engine/regobj

Catalog objects and typed values as SQL text, and the way back: the
object-identifier types, type declarations, typed literals, `aclitem`
I/O and object identity. Step 21 of the engine split created it (21b moved
the clean set; 21c moves the captured input resolvers). The package sits at
layer L11 of
[ENGINE-SPLIT.md §3](../../../docs/ENGINE-SPLIT.md#3-target-architecture):
it imports `analyze`, `authorization`, `catalog`, `exec`, `expr`, `ids`,
`pgcatalog`, `plan`, `routine`, `session`, `parser`, `sqlstate`, `sqltype`
and the standard library, and never the engine, `stored` or an upper
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
  `RoleContextFormatter`.
- **reg\* input helpers** (`regclass_input.go`, `regrole_input.go`,
  `regoperator_groups.go`, `regproc_lookup.go`): `ClassNumeric`,
  `ClassQualifiedName`, `RoleInput`, `CollectOperatorGroups`,
  `VisibleOperators`, `SelectRoutine`, `SelectExactRoutine`,
  `RoutineIdentityByOID`, `RoutineDescriptorOwner`.
- **Dump spellings, stored-value walkers and remap**
  (`regclass_dump.go`, `regproc_persistence.go`,
  `regoperator_persistence.go`, `regts_persistence.go`,
  `regtype_persistence.go`): `DumpOperatorOutput`, `DumpTextSearchOutput`,
  `DumpAliasOutput`; `OperatorLiteral`, `RoutineLiteral`;
  `Walk{Routine,Operator,TextSearch}References(At)`,
  `ClassReplayReferences`; `Remap{Class,Routine,Operator,TextSearch,Alias}Datum`.
- **Type declarations and literals** (`catalog_literal.go`):
  `TypeDeclaration(At)`, `ColumnDeclaration`, `Literal`,
  `LiteralWithACL(At)`, `ConstantLiteral`.
- **Search-path visibility of named objects** (`namespace_visibility.go`):
  `NamedObjectVisible`, `CollationCandidate`, `SelectCollation`,
  `VisibleCollation`. These are name-visibility rules, not MVCC visibility.
- **`aclitem` I/O** (`aclitem_input.go`, `aclitem_output.go`,
  `aclitem_dump.go`, `acl_empty.go`): `ACLItemInput`, `ACLItemOutput`,
  `ACLItemValidationOutput`, `ACLItemContextFormatter`,
  `ACLItemOutputContext`, `PreflightACLItemValue`, `ContainsEmptyACL`.
- **Object identity** (`object_identity.go`): `pg_identify_object`,
  `pg_describe_object` and `pg_get_object_address` through
  `IdentifyAddress`/`ObjectIdentity`, and the input helpers
  `ObjectInputArray`, `ObjectInputClass`, `ObjectListLength`,
  `ObjectAddressTextArray`, `ObjectAttributeName`, `ObjectRelationKind`,
  `OwnedAddressByName`. Rule identity is read through
  `(*catalog.Relation).VisitRules`.

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
  permission decision or a catalog snapshot.
- No mutex; nothing takes `Engine.mu`. Callers that need the engine lock
  (`Engine.capture*`) hold it themselves.

## What stays in the engine

- The statement caches and formatters: `presentationCaches` and the
  `StatementScope.Format*` methods behind `exec.Host`. They are keyed by
  `exec` keys and filled through the spellings above.
- The constructors that read engine state: `Engine.captureRegclassOutput`,
  `Engine.captureACLItemInput`, `Engine.captureRegObjectRowsValueLocked`,
  `Engine.regclassLocked`, the other `Engine.capture*Input` methods, and the
  `analyze.Host` resolver methods (forwarders).
- The captured input resolvers `capturedRegclassInput`,
  `capturedOperatorInput`, `capturedRoutineInput`, `capturedRegTSInput`,
  `capturedRegObjectInput` and `capturedRegtypeInput`: they hold an
  `AuthorizationView` until 21c.
- `regtypeAnalysis` and `Engine.regtypeAnalysisLocked`/`ValidLocked`: the
  prepared-statement regtype analysis cell is statement state, not SQL text.
- `preflightACLItemDump` (Dump/Restore, step 30), `aclitem_remap.go` and
  `principal_remap.go` (Restore, step 30), and `scalarRenderer` with its
  `record*` methods (`stored`, step 22).

## Testing

```sh
make check RUN='^TestVisibilityInquiryCollationSelection$'
make check RUN='^TestReference(Reg|ACLItem|ObjectAddress|FormatRegclass|SequenceRegclass|ArrayRegclass|RLSPolicyRegtype|SQLCursorRegtype|UpdateReturningCapturedRegclass|PGDependRegclass|PartitionConstraintsRegclass)'
make check RUN='DumpRoundTrip|NativeControls'
```

Add `REFERENCE=1` for the real PostgreSQL oracle. The engine unit tests of
this code need an `Engine` and stay in `internal/engine`
(`TestObjectAddress*`, `TestRegTS*`, `TestRegOperator*`, `TestRoutine*`
in `regproc_test.go`, `TestRegrole*`, `TestRegclass*`, `TestACLValues*`,
`TestACLItem*`, `TestRegObjectAlias*`).
