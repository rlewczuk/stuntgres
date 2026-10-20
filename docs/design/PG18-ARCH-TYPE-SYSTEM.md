# PG18-ARCH-TYPE-SYSTEM — Typed values and coercion

## 1. Decision and acceptance

Adopt one future `internal/sqltype` seam for resolved identity/descriptors, immutable nullable values, codecs and coercion plans. Parser owns raw syntax/positions; the existing engine owns binding/evaluation/storage; root driver adapters own `database/sql` conversion. Extend existing catalog, binder, Snapshot and controls: no parallel allocator, store, binder or parser/engine type lists. Production dependencies remain standard-library/module only.

**Design alone enables no executable consumers: implementation requires PG18-TYPE-CORE directly or transitively.** This part changes no production/test code, grammar, catalog edges or other designs. Interfaces below specify logical internal contracts, not public APIs or exact struct layouts. They do not implement new types, expressions, casts, overloads, collation, SQL PREPARE or a wire server.

Normative evidence is the PostgreSQL 18 manual (currently identifies **18.6**) and release C/catalog sources linked in §11. Preserve the existing PostgreSQL **18.0**, UTF8/C reference image:

```
postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854
```

`reference/fixture/fixture.go` pins that image; `reference/fixture/lifecycle.go` asserts version/locale/encoding. Minor-specific behavior belongs to PG18-REF-ORACLE-18-6, never a guessed image. Local PostgreSQL 20devel is research-only; Go/ramsql implementations are not PostgreSQL semantic evidence.

The user explicitly selected **“Use PostgreSQL 18 rules”** for three identified conflicts: typmod-sensitive input (§2), contextual unknown output parameters (§3/§5), and `pg_typeof` REGTYPE metadata (W1). This supersedes only the prior universal input-without-typmod rule, blanket unresolved-parameter/no-text rule, and W1 TEXT metadata claim. Independent destination sizing, unresolved-parameter failure after contextual resolution, and all other acceptance remain. Shared plan/conflict history records rationale and deferred implementation.

| Requirement | Contract | Independent expectations |
|---|---|---|
| Internal/wire values, serialization | §2 | W4/W7/W10 |
| Identity/descriptors/typmods/NULL/unknown/codecs | §§2–3 | W4/W5/W7/W8 |
| Cast contexts, ordered common type | §4 | W1/W3/W5/W8 |
| Overloads and parameter inference | §5 | W1–W3/W6 |
| Equality/order/hash/collation | §6 | W4/W9 |
| Prepared and result metadata | §7 | W6/W7/W10 |
| Ownership/state/publication/interleavings | §§2–3/§7–§8 | W4/W6/W10 |
| Extensions, complete cutover, bounded owners | §§3–9 | Inventory and slice exit witnesses |
| Required assignment-vs-overload, exact/ambiguous/no-match, metadata | §§4–5/§7 | W1–W3/W6/W7 |

Current baseline already has catalog descriptors/name indexes, identity-keyed relation storage and parser ObjectName. It still uses `parser.Type`, parser columns, driver-valued rows, enum/where-bool coercion, unchecked primitive comparison, enum metadata and AST/count-only prepared handles. `fixture.Diagnostic`/extraction/assertion already support full fields; new features need exact fixtures, not a new diagnostic transport. §8 names current callers rather than the obsolete Engine.tables inventory.

## 2. Values, codecs and adapter boundary

The logical `sqltype` contracts are:

```go
type ID uint32 // zero invalid; catalog type-object OID projection
type Usage struct { Type *Descriptor; Typmod int32; Collation CollationRef }
// Value has private descriptor, NULL flag, inline int64/bool/string slots,
// and a checked immutable extension payload; copying does not mutate payload.
func (v Value) Type() *Descriptor
func (v Value) IsNull() bool

type Codec interface {
    Input(ctx context.Context, text string, inputTypmod int32) (Value, error)
    Output(ctx context.Context, value Value) (string, error)
    SQLLiteral(ctx context.Context, value Value) (string, error)
}
```

`sqltype` imports neither parser nor engine nor `database/sql/driver`; SQL error identity reuses `internal/sqlstate`. Descriptors expose read-only data. Zero Value is invalid, not NULL. Typed NULL requires a defined concrete descriptor and no meaningful payload. Baseline integers remain inline signed int64 without per-cell boxing. Extension constructors validate descriptor/payload compatibility; no public `any` setter or unchecked assertion.

Usage is occurrence-specific (expression/column/parameter/result): modifier and collation are not additional type identities. Typmod -1 means unspecified; its encoding/validation/formatting belong to the descriptor's modifier provider, not a consumer guessing lengths from an integer.

**Input and sizing are separate responsibilities, not a universal two-pass interpretation rule.** The coercion caller selects inputTypmod by call context using descriptor-owned modifier semantics: an UNKNOWN Const normally receives -1, but interval input receives the meaningful base interval field typmod (`parse_coerce.c:coerce_type`). A typed value converted by CoerceViaIO instead receives -1 (`execExpr.c`), then destination sizing/checks run (`coerce_to_target_type`); the unknown-constant exception does not apply to IO casts or parameter import. Domains select base input and retain domain checks/typmod semantics at their coercion boundary. This is argument-selection policy for the existing three-argument Input, not another interface or policy abstraction. Final sizing retains assignment/explicit context; it cannot recover interpretation lost during unknown-constant input. Release sources are linked in §11. No interval implementation is added to TYPE-CORE.

Output is type text output, not wire export or SQL quoting. Boolean text output is t/f where required; baseline driver export stays bool. SQLLiteral emits a reparsable type-correct expression with feature-owned quoting/qualification. Central NULL formatting does not invoke input: a typed INSERT destination permits `NULL`, while a standalone typed NULL needs a type annotation. New stored types must be serializable/restorable by their owner; never silently omit unsupported values from a complete dump.

Root-internal adapter contracts remain `ImportParameter(ctx, raw driver.Value, target Usage) (Value,error)`, `ExportValue(ctx, Value) (driver.Value,error)` and `ScanType(*Descriptor) reflect.Type`. Parameter descriptor controls input interpretation: a Go int64 does not determine SQL int8. DefaultParameterConverter/Valuer processing and count/name/ordinal validation remain adapter concerns; ordinary argument/context/I/O errors are not fabricated SQLSTATE. Unsupported SQL features/types remain owned SQL errors, not fallback text.

Ownership invariants:
- Detach mutable incoming bytes once before retention; baseline byte-to-string conversion supplies immutable ownership. Codecs retain no borrowed mutable buffers. Share immutable strings/scalars without redundant copies.
- Freeze extension element/dimension/member payloads at construction; accessors are read-only or return owned copies. Wire bytes are owned independently of retained rows.
- Published descriptors, codecs, plans and operation handles are immutable and retain their catalog/provider lifetime. They cache no arguments or execution-local state; old cursors survive Reset/Restore/replacement.
- Snapshot remains the one retained projection. `Values(index,dst []sqltype.Value)` copies projected values to cursor-owned reusable scratch; rows.Next exports into the supplied wire destination, not a second store or per-cell rebind. No whole-snapshot deep copy.
- Baseline exports: int4/int8→int64, text→string, bool→bool, typed NULL→nil; metadata retains identity despite nil payload.

## 3. Identity, syntax and resolution

Descriptor logical fields: catalog identity/namespace/name; canonical SQL spelling and driver type name; kind, category/preferred status; defined/shell state; base/domain, element/array and feature-owned composite links; default collation; codec; optional modifier and operation providers. Physical `pg_type.typlen`, alignment/storage and wire size are not Go struct size. Length/precision-scale are independently supplied metadata.

| Type | Fixed ID | Baseline SQL / driver name | Category / preferred |
|---|---:|---|---|
| bool | 16 | BOOLEAN / BOOL | B / yes |
| int8 | 20 | BIGINT / INT8 | N / no |
| int4 | 23 | INTEGER / INT4 | N / no |
| text | 25 | TEXT / TEXT | S / yes |
| unknown | 705 | analysis only, no finalized driver name | X / no |

A retained catalog-backed view exposes `LookupType(ID) (*Descriptor,bool)`. Name resolution supplies identity; missing lookup fails at its owning name-resolution boundary, never returns text. User IDs come from the catalog allocator and are checked relationally, not pinned to another server's allocation sequence. Builtin IDs/names are exact.

[CATALOG-IDENTITY §§2–4/§6–7](PG18-ARCH-CATALOG-IDENTITY.md) remains authoritative: ID is only its type OID projection; descriptors/plans retain ObjectRef ownership, epoch and DefinitionRevision. Equal numeric IDs do not authorize cross-engine/database/epoch use. Adapters expose catalog data without sqltype importing engine or introducing another registry. A new revision is published, never mutation; retired descriptors remain usable by captured cursors, not new current-catalog binds. Reset/Restore may reuse numeric user IDs in a new epoch without validating stale refs. Catalog validates/stages links and publishes once.

Parser.Column.Type becomes raw TypeName (canonical baseline spelling, raw modifier constants, source position). The prior illustrative `{Name string; Modifiers []Value; Pos Position}` is a baseline shape, **not** permission to flatten qualified names: qualified type/name consumers reuse CATALOG ObjectName components/normalization and their own grammar. Keep existing relation ObjectName. `parser.Value` retains literal kinds, fields, parameter index and Position as syntax, not runtime payload or another resolved enum. Engine validates permitted modifier constants and passes normalized inputs to providers; providers do not import parser.Value. Generalized qualification/typmods/expression grammar belongs to its feature.

| State transition | Contract / failure |
|---|---|
| SQL→AST | Whole-input syntax; retain original SQL and positions, never execute a valid prefix |
| AST→analysis | Unknown strings/NULL, known primitive literals, unresolved parameters remain analysis-private |
| Contextual resolution→bound Usage/plans | Select defined descriptors/coercion/call/key handles; failed resolution publishes no executable description |
| Finalization→executable frame/result/row usage | All concrete; unknown/zero cannot enter publication |
| Evaluation/coercion→Value | Typed NULL or valid payload; input/sizing/domain/constraint/cancellation failure publishes no row |
| Capture→wire/SQL serialization | Pair immutable metadata/rows; output failure cannot mutate retained state |

Unknown strings feed selected target input; NULL gets target identity without input. Standalone unknown SELECT/RETURNING targets finalize to text after other contexts are exhausted, **including a bare unresolved parameter target**. Contextual INSERT SELECT/set arms defer output fallback. Parameters elsewhere do not get unconditional text: unresolved types after the applicable contextual rules fail 42P18 (e.g. `$1 IS NULL`). Release `resolveTargetListUnknowns` and variable parameter hooks establish this distinction (§11).

TYPE-CORE retains parser.integerValue signed-int64 bounds; expression analysis classifies in-range int4, otherwise int8. Larger/decimal numeric literal classification is later owned work, not enabled here. Preserve Position.Offset source evidence and existing Position.Character; original SQL converts byte offsets to PostgreSQL one-based character positions when required. Unknown position is not position one; no substring search or UTF8 byte-counting as characters.

Extension invariant: register one descriptor with checked constructors, codec, optional modifiers/casts/operation families via the catalog. Shell types cannot become concrete values before definition. Domains retain identity/check ownership even when selection peels to base; arrays retain element identity/dimensions/bounds/element NULLs; composite members follow catalog column identity. Adding types must not add synchronized parser/engine/rows switches. Missing codec/provider is an error, not identity conversion or fmt serialization.

## 4. Coercion and common type

Logical contracts: `PlanCoercion(source Usage,target Usage,context) (CoercionPlan,error)`, `ResolveUnknown(text,isNull,target Usage) (CoercionPlan,error)`, immutable `CoercionPlan.Apply(ctx,Value) (Value,error)`, and `CommonType(ordered []Usage,construct) (Usage,[]CoercionPlan,error)`. Planning is scoped to a retained catalog/type view, never global mutable state. Plans retain source/target, context, destination typmod/collation, provider and dependencies; no execution arguments.

| Minimum cast context | Implicit | Assignment | Explicit |
|---|---|---|---|
| Implicit | yes | yes | yes |
| Assignment | no | yes | yes |
| Explicit | no | no | yes |

CastSpec records directional source/target IDs, minimum context and method/provider identity. Methods: identity (no allocation/call if no sizing), binary relabel (asserted compatible immutable representation, not same Go kind), real function provider, or IO (source Output→target Input with inputTypmod=-1, not SQLLiteral/fmt/wire export). Equal ID still requires destination modifier enforcement. Providers receive destination typmod and explicitness; combine conversion/sizing only when supported, otherwise size afterward. Unknown-constant input selects its argument as in §2, separately from IO input followed by destination sizing. Unknown plans own raw input, materialize constants at bind/evaluation stage and NULL without input; they do not feed unknown Values to Apply. Typed plans validate source descriptor/payload; parameters materialize in fresh frames against parameter Usage (Typmod -1), with occurrence sizing afterward, never by borrowing an unknown-constant destination typmod.

Resolve expressions **before** destination assignment. Overload admission uses Implicit only, never assignment IO-to-string or integer narrowing. Automatic IO to string-category targets is assignment-eligible; from strings explicit-only, subject to registered/documented overrides. Unknown target input is not an implicit text→every-type edge. No graph-transitive shortest-path casts or retrying overloads after input failure.

Common type follows [§10.5](https://www.postgresql.org/docs/18/typeconv-union-case.html): (1) identical nonunknown types win, including exact domain; (2) otherwise peel domains; (3) all unknown→text, otherwise ignore unknown during choice; (4) unlike known categories fail; (5) first known candidate moves left-to-right only for one-way implicit conversion, stopping choice at preferred type; (6) every input must convert implicitly to chosen type, including inputs after that stop. Return ordered plans. CASE considers ELSE before THEN; nested set operations resolve pairwise, other documented constructs together. Preserve domain only for exact-domain inputs. Retain typmod only if **every input already has chosen type and the same typmod**, else -1; matching modifier numbers on different types do not suffice. Collation derivation is independent; destination sizing follows context. Generalized CommonType execution is PG18-FUNC-CONDITIONAL, not TYPE-CORE.

| Failure | SQLSTATE (wording/source owned by consumer) |
|---|---|
| Recognized unimplemented feature | 0A000 |
| Supported explicit pair without cast | 42846 |
| Assignment/common-type incompatibility | 42804 |
| Callable no match / nonunique best | 42883 / 42725 |
| Malformed input / numeric range / modifier overlength | 22P02 / 22003 / 22001 |

Failed providers/domain/constraint checks/cancellation prevent publication. Absent internal capability is never successful no-op; ordinary boundary errors remain separate.

## 5. Calls and parameters

Candidate contains callable catalog identity/kind, visibility/rank, formal IDs, result derivation, strictness, normalized default/variadic argument mapping and native/routine provider identity. Catalog supplies visible candidates/qualification and mappings, authorization privileges, native/routine owners evaluation. Type selection invents neither search paths nor another callable catalog.

`ResolveOperator(candidates,operands []Usage)` and `ResolveFunction(candidates,arguments []Usage)` return BoundCall/error. BoundCall retains selected identity/signature, ordered coercions, result Usage, **input/operation collation separately from result collation**, and dependencies. Results follow signature/polymorphic constraints, not Go kinds; unsupported polymorphism stays unsupported. Strict evaluation yields typed NULL where appropriate; nonstrict semantics remain provider-owned.

Normalize once: identical visible signatures use path precedence; different signatures compete equally. Effective variadic/nonvariadic ties prefer earlier path, or nonvariadic in same schema; indistinguishable same-schema default-expanded signatures stay ambiguous without a better match. This adds no named/default/variadic syntax.

Entry rules differ: operators first exact-match; binary known/unknown assumes known type for exact check, then known-domain/unknown tries both base operands. Two unknowns/unary unknown cannot use that shortcut. Functions first exact (unknown never exact), then eligible one-argument type-name conversion (unknown literal, binary compatibility or documented string IO; composite→string needs CAST/::). This is a cast decision, not assignment admission to ordinary functions.

Shared best-match filter (§10.2/10.3): discard non-implicitly-convertible candidates (unknown provisionally any); domains→base; maximize exact positions; maximize preferred target positions where conversion is needed; at unknown positions bias to string if present, otherwise require agreed category, then preferred within category; finally when known inputs all share one type, try it for unknowns and require one survivor. Retain candidates where the documented preference test retains them if none survive; never select registration order. No match 42883, ambiguity 42725. Only selected target input runs; bad input does not change overload.

Engine parameter contracts remain `ParameterInfo{Count int; Types []Usage}` indexed n−1, statement-local `ConstrainParameter(number,expected Usage,sourceOffset) error` and `FinalizeParameters() (ParameterInfo,error)`. Sparse tracking validates references/gaps before count-sized allocation. First contextual resolution fixes ID; parameter Usage has Typmod -1/default collation, not destination modifier. Each occurrence owns its conversion/sizing. Output-text finalization is one contextual constraint, not unconditional parameter default.

An already-resolved occurrence converts from fixed ID and may assign to another compatible destination. Distinct unresolved occurrence copies forced incompatibly before reconciliation fail 42P08; preserve occurrence/source state, not overwrite a map. Finalization rejects unresolved/gaps 42P18 and invalid references 42P02 without publishing. TYPE-CORE preserves narrow unsupported mixed-destination 0A000 until generalized inference's owner; no full compatibility claim.

Direct/prepared paths share binder. Prepared types are independent of execution values; every execution imports a fresh frame against retained description and applies occurrence plans. No shared inference mutation, retained borrowed buffers or type/value leakage. Assert IDs/modifiers/collation/plans, not result equality alone.

## 6. Comparisons, keys and collation

SQL comparison is a bound callable returning nullable Boolean; strict NULL comparison is bool-typed NULL and WHERE accepts only true. Key operations are independently optional nonnull `KeyEqual(ctx,a,b) (bool,error)`, `KeyCompare(ctx,a,b) (int,error)`, `KeyHash(ctx,v,seed uint64) (uint64,error)`. Immutable family handles bind compatible operand descriptors, collation/provider generation once. Query policy owns grouping NULL equivalence and sort placement: preserve baseline ASC NULLS LAST / DESC NULLS FIRST. Do not infer default key families from SQL operator spelling.

Family invariants: compare-zero agrees with equality; equal keys hash equally for one seed including supported int4/int8; compatible implicit/binary conversions preserve this; collisions still require equality. Equality does not imply ordering/hash; absent requested hooks fail at their consumer, never reflection/Go-interface comparison/fallback hashing. Hash bytes are not pinned to PostgreSQL implementation bytes.

CollationRef retains catalog ID, derivation (None/Default/Implicit/Explicit/Indeterminate), diagnostic source offset and catalog/provider lifetime; enum names must be disambiguated from coercion contexts. Noncollatable usages carry None. Collatable constants/parameters use default; columns their declared collation. Combination (§23.2.1): conflicting explicit IDs 42P21; agreeing explicit overrides implicit/default; nondefault implicit overrides default; agreeing IDs propagate; conflicting nondefault implicit→Indeterminate. Insensitive concatenation can propagate it; a runtime sensitive operation fails 42P22. Required common-collation analysis may instead reject an implicit conflict with 42P21 (`select_common_collation(none_ok=false)` in release parse_collate.c); diagnostics remain consumer-specific. Collatable result uses combined input collation, or its descriptor default with no collatable input. A noncollatable bool result can still require input collation for comparison.

C/POSIX identities differ despite byte-order equivalence. Missing providers never silently become C. Provider registration truthfully records version/encoding/locale/determinism and immutable handles. Comparison/equality/hash use one semantic equivalence, including nondeterministic equality and compatible hashing when supported. No ICU execution/dependency is implied; provider owners respect standard-library production policy. No index/transaction/lock algorithm is specified.

## 7. Descriptions and prepared boundary

```go
type ColumnDescriptor struct {
    Name string
    Usage sqltype.Usage
    Nullable, NullabilityKnown bool
    OriginTable uint32
    OriginAttribute int16
}
type StatementDescription struct {
    Parameters ParameterInfo
    Results []ColumnDescriptor
}
```

Describe returns this logical metadata instead of ParameterInfo alone; Result.Columns uses ColumnDescriptor (now `catalog.Column`). **Its executable signature still consumes SESSION's `*Operation` lease/coherent context when that capability is present**; this shape is not a replacement for SESSION Acquire/Done. SESSION owns lifecycle/selected database, CATALOG owns resolution/validity. Descriptions/slices are immutable. Origins are catalog relation ID/stable attribute number; zero means no physical origin. Optional descriptor length/precision-scale is known only when meaningful, not inferred from Go width/physical typlen. Parameter OIDs versus row typmods/origins are logical protocol evidence, not a wire server; format code is transport choice.

Baseline physical columns preserve INT4/INT8/TEXT/BOOL, ScanTypes int64/int64/string/bool and known declaration nullability. Expressions have unknown nullability unless proved; typed NULL retains ID/meaningful typmod. Unknown output finalizes before description publication. Optional RowsColumnTypeLength/PrecisionScale adapters only when providers answer; otherwise absence.

PrepareContext retains AST/original SQL/StatementDescription, NumInput reads Parameters.Count; Exec/Query still converge through conn.execute. Existing before-execution rebind remains the seam; pair result rows and generation captured for that execution, never mutate cursor metadata after replacement. Per-execution frames are not descriptions.

Dependencies include objects/type/cast/callable/collation/provider refs. CATALOG §6 owns owner/epoch/revision/environment validity, fixed parameter/result contract and complete rebind installation; its illustrative BoundPlan parser columns consume this resolved shape, not another enum. PG18-DRIVER-PREPARED/PREPARED-REBIND own SQL PREPARE lifetime/search-path/DDL cached-result policy. Failed bind never executes stale pointers or partially installs metadata. Type design specifies no competing invalidation policy.

## 8. Clean cutover and publication

All edits below are **future implementation deferrals**, not fixed by this design. Search the actual tree again before implementation; symbols, not old line numbers, are anchors.

| Current paths/symbols | Later migration / retained boundary |
|---|---|
| parser/ast.go Type/SQL/DatabaseType/Column.Type; grammar.y typ/type productions; parser.go integerValue; generated grammar_gen.go | Delete resolved enum/naming helpers; raw TypeName preserving ObjectName, Value/positions/parameter indices/signed bounds. Regenerate from grammar source, no generated-only conventions. |
| engine/catalog.go relationDescriptor.Columns (now `catalog.Relation`)/relationStorage.rows (now `storage.Relation.Versions`), relationProvider; engine.go CREATE descriptor construction | Resolved column descriptors and typed rows through existing provider/stage/identity storage; preserve attributes/catalog validation, no table-map restoration. |
| engine.go ParameterInfo/Result/binding/Describe/describe/bind/Execute/resolve (`binding` and `ParameterInfo` now in [`analyze`](../../internal/engine/analyze/README.md)) | Usages/coercion/call/key handles and StatementDescription; descriptor validation replaces enum upper bounds/where bool; sparse inference, staged execution, current resolution seam. |
| engine.go prepareArgs/parameterColumn/insertValueError/notNullError; value.go input/parameter/literal/failingRowDetail | Root normalization/import and typed frames; diagnostic lookup stays diagnostic-only. Codecs/descriptor names/formatting replace primitive resolved-type switches; preserve consumer source/error context and ordinary errors. Delete obsolete helpers after all callers migrate. |
| snapshot.go Snapshot/Values/compare/selectRows | Typed rows/predicate and reusable typed destination, bound operations replacing unchecked assertions; preserve capture/projection/filter/sort without whole-snapshot copy. |
| control.go Dump/Restore/dumpRelation | Descriptor formatting/SQLLiteral replace Type.SQL and primitive serialization; preserve separate schema/name quoteIdentifier, deterministic emit/errors, staging binder and ReplaceCatalog; forbid parameters. |
| conn.go PrepareContext/execute/ExecContext/QueryContext; stmt.go stmt/NumInput/Exec/Query | Retain AST/source/description and shared import/bind path; preserve exact opaque DSN/Driver isolation, close/context checks and session/catalog ownership seams. |
| rows.go rows/newRows/Next/three metadata methods | ColumnDescriptor/typed scratch/ExportValue/descriptor adapters; remove parser import/type switch/default bool; preserve EOF/Close/context. |
| engine_test.go tuples/Describe/invalid type/qualified metadata/control tests; catalog_test.go `catalog.Relation` fixtures | Migrate internal representation/constructors and add descriptor/plan invariants without changing independently expected SQL results. |
| parser_test.go TestScriptAndLiterals | Replace `create.Columns[3].Type != Int8` with a raw TypeName canonical `bigint` assertion for INT8 syntax; preserve identifier/nullability, literal payload, parameter and source-position expectations. |
| driver_test.go direct/prepared/metadata/retained-query tests; reference/sessions/fixtures_test.go and reference/type_resolution/diagnostics_test.go rows/types/diagnostic fixtures | Preserve independent public outcomes; extend exact feature fixtures using existing full-field helpers, not parity alone or incidental source-wiring counts. |

Caller inventory pattern over Go files: `parser\.(Type|Column|Value|Int4|Int8|Text|Bool)|\.DatabaseType\(|\.SQL\(|ParameterInfo|\.Values\(`. Supplement inside `internal/parser/*.go` with `\b(Type|Int4|Int8|Text|Bool)\b|\.Type\b` to find unqualified enum/Column.Type assertions, including TestScriptAndLiterals. Include catalog fixtures/diagnostic formatting and follow generated parser callers through grammar source. Raw parser.Value/Column syntax callers may remain; resolved enum aliases/reexports/deprecated helpers/shims may not.

Publication boundaries (unchanged):
1. INSERT stages every tuple/input/coercion/constraint/cancellation check and appends once after full success; CREATE validates/stages catalog before publication. Later-tuple errors publish no fragment.
2. SELECT captures immutable rows, projection and paired metadata under mutex; filter/sort after unlock. Appends do not alter captured count/payload; no retained scratch arguments.
3. Dump captures descriptors/rows before writer I/O. Partial output on error is possible, committed mutation is not. No target lock over external I/O.
4. Restore reads/parses/executes in staging Engine through same binder/codecs, rejects parameters, installs via ReplaceCatalog only after complete success/final cancellation check. Reader/input/constraint/cancellation failures preserve target.
5. Replacement publishes new descriptor/provider lifetime, never mutates old; old cursors retain generations, new binds validate current ownership/epoch.
6. Concurrent/repeated executions have private frames/scratch and shared immutable descriptions only; Close/cancellation preserve adapter semantics. These are capture/retention/atomic-publication boundaries, not session/transaction/lock/retry algorithms.

## 9. Bounded implementation ownership

The first three slices are **one PG18-TYPE-CORE capability**; partial work cannot mark it done. This map prescribes no new catalog records/edge edits.

| Owner / session slice | Consumed contract and independent exit |
|---|---|
| TYPE-CORE representation | This design + PG18-DRIVER-ERROR-FIELDS; descriptor/Value/codec and baseline raw type-name binding. Actual CREATE/INSERT/SELECT int4/int8/text/bool/NULL/unknown, not registry scaffold; no arrays/new families. |
| TYPE-CORE coercion/inference | Assignment/input subset and one direct/prepared describe/execute path; W4/W6 widths/IDs/errors/atomicity. No generalized common type/overload/explicit casts. |
| TYPE-CORE completion | Snapshot/export/metadata/Dump/Restore and every caller; W7/W10 retention/round trip/error preservation. |
| PG18-EXPR-CORE | Implemented TYPE-CORE and expression contracts, one executable bound/evaluated seam. |
| PG18-CAST-EXPLICIT | CAST/::/function-style explicit baseline plans after expression seam; W2/W3 success/no-cast failure, no implicit shortcut. |
| PG18-FUNC-COMPARE-OPS | Expanded comparison/cross-width signatures; W4/W9 SQL/key distinction. |
| PG18-FUNC-CONDITIONAL | Ordered common-type execution; W5 unknown/pairwise/domain/category boundaries. |
| PG18-FUNC-INFO-TYPE-DESCRIBE | Type identity/typmod formatting and typed NULL; W1 REGTYPE and W7. |
| PG18-DRIVER-PREPARED / PG18-DRIVER-PREPARED-REBIND | SQL lifetime/invalidation/fixed-result policy through same binder/ColumnDescriptor; W6/W10. |
| PG18-ROUTINE-CORE | Real scalar invocation/result conversion; exact signature/result, routine frames/auth remain owned there. |
| PG18-FUNC-OPERATOR-USER | Catalog-visible signatures and real selected provider; W2 exact/ambiguous. |
| PG18-CAST-DEFINITION | Registered directional edges and real binary/function/IO providers; context matrix, not metadata-only success. |
| PG18-TYPE-ARRAY | Immutable element/dimension/bounds/NULL payload/codecs through same seam. |
| PG18-TYPE-CHARACTER | Typmods/padding/truncation/comparison; W1/W8, outside TYPE-CORE. |
| PG18-EXPR-COLLATION | Provider identity/derivation and input collation/key handles; W9. |

Typmod-sensitive interval input is an obligation for the later interval/type-family owner, not a fifth TYPE-CORE type or a new task. No type-design edge is required for PG18-DRIVER-ERROR-FIELDS, PG18-SYNTAX-IDENTIFIERS or PG18-TYPE-BOOLEAN-DIAGNOSTICS: baseline diagnostic/lexical/bool seams remain independently implementable. Catalog/session/auth/routine ownership stays with their contracts.

## 10. Independent architectural witnesses

These are **expected**, not exercised behavior. Future owner fixtures use fresh state, direct/prepared public API and bounded real PostgreSQL where applicable. Unordered queries compare bags/multiplicities. Exact diagnostics include SQLSTATE, English primary/detail/hint, one-based character positions and conditionally present fields; absent fields stay absent. `[INFERENCE]` means a derived expectation requiring owner release capture, not a local-20devel/runtime claim.

### W1 — Assignment distinct from overload

```sql
CREATE TABLE vv(v character(20));
INSERT INTO vv SELECT 'abc' || 'def';
SELECT v, octet_length(v) FROM vv;
SELECT pg_typeof('abc'||'def'), pg_typeof(v) FROM vv;
```

[§10.4 Example 10.9](https://www.postgresql.org/docs/18/typeconv-query.html): affected=1, `abcdef` plus fourteen spaces, integer octet_length 20. Unknown operands select text/text concatenation→TEXT result→assignment binary text/bpchar relabel→destination sizing with explicit=false. Binary relabel inserts no real conversion function, sizing still runs; destination cannot select operand overload. Supplemental displayed values `text`,`character` [INFERENCE from identity]; **both result types are REGTYPE/OID2206**, not TEXT (release pg_proc.dat pg_typeof→regtype; [pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat) fixes regtype=2206, whereas 2202 is regprocedure). Owning expression/string/character/type-description features, not TYPE-CORE.

### W2 — Exact and ambiguous operator

```sql
SELECT text 'abc' || 'def';
SELECT ~ CAST('20' AS int8);
SELECT ~ '20';
```

§10.2: `abcdef`/TEXT with chosen text/text and right input plan; -21/INT8 exact unary signature; final **42725**, primary `operator is not unique: ~ "unknown"`, complete **Hint** `Could not choose a best candidate operator. You might need to add explicit type casts.` First Hint sentence is not Detail. Ambiguity invokes no provider; optional fields/position require owner 18.0 capture.

### W3 — Function admission is implicit, not assignment

```sql
SELECT substr('1234',3);
SELECT substr(varchar '1234',3);
SELECT substr(1234,3);
SELECT substr(CAST(1234 AS text),3);
```

§10.3: first/second/fourth `34`/TEXT, selected text input or implicit varchar/text relabel. Third **42883**, primary `function substr(integer, integer) does not exist`, Hint `No function matches the given name and argument types. You might need to add explicit type casts.` Assignment IO integer→text cannot admit candidate; failed bind publishes no call.

### W4 — Width/input/atomicity

```sql
CREATE TABLE t(i INTEGER);
INSERT INTO t VALUES(1),(2147483648);
SELECT i FROM t WHERE i=2147483648;
INSERT INTO t VALUES('not an integer');
INSERT INTO t VALUES(TRUE);
```

22003/no rows for multirow overflow, empty predicate result (not assignment narrowing), 22P02 malformed input, 42804 bool assignment. Primary expectations `integer out of range` and `invalid input syntax for type integer: "not an integer"` [INFERENCE pending release capture]. Int4 −2147483648/2147483647 and int8 −9223372036854775808/9223372036854775807 succeed; textual int8 beyond either bound fails 22003. Later bad tuple, required NULL (23502), or cancellation preserves pre-statement bag; native cancellation remains errors.Is(context.Canceled), not fabricated SQLSTATE. No decimal expansion.

### W5 — Output/common-type order

```sql
SELECT 'Hello World';
SELECT text 'a' UNION SELECT 'b';
SELECT 1.2 UNION SELECT 1;
SELECT NULL UNION SELECT NULL UNION SELECT 1;
```

TEXT `Hello World`; TEXT bag {a,b}; NUMERIC bag {1.2,1}; final 42804/`UNION types text and integer cannot be matched`. Inner NULL pair resolves TEXT before outer union. Unit expectations: all unknown ID25; ordered one-way implicit candidate; category failure; exact-domain preservation versus domain/base→base; CASE ELSE first; chosen-ID-and-same-modifier retention, otherwise -1; final conversion check includes inputs after preferred stop. Numeric/general constructs are future owners.

### W6 — Parameter identity and fresh frames

```sql
CREATE TABLE p(a INTEGER,b TEXT,c INTEGER);
INSERT INTO p VALUES($1,$2,$1);
```

Description Count2/IDs[23,25], Typmod −1/default collation. Independent direct/prepared `(int32(7),"x")`→affected1/(int64(7),"x",int64(7)); next prepared `(8,"y")` adds only (8,"y",8), final two-row bag and no leakage.

```sql
PREPARE p1 AS INSERT INTO p VALUES($1,$2,$1);
SELECT parameter_types FROM pg_prepared_statements WHERE name='p1';
PREPARE bare AS SELECT $1;
PREPARE bad AS SELECT $1 IS NULL;
```

p1 `{integer,text}` independent of later values [INFERENCE]; bare parameter and result resolve TEXT/ID25 by output context [INFERENCE from release target/parameter hooks]; bad remains **42P18**, primary `could not determine data type of parameter $1` [INFERENCE]. Gap `$2` without resolved `$1` fails 42P18 before allocation; invalid reference 42P02. No unconditional text default.

Unit failure: distinct unresolved `$1` copies forced int4/text before reconciliation→42P08, primary `inconsistent types deduced for parameter $1`, Detail `integer versus text` [INFERENCE, not captured SQL]. Already-resolved int4 then assignment to text keeps ID23 and records conversion rather than overwrites description. Assert occurrence/source/plans. Baseline mixed destinations remain unsupported 0A000; generalized success is later owner acceptance.

### W7 — Result metadata

```sql
CREATE TABLE metadata(i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN);
INSERT INTO metadata VALUES(1,2,'x',FALSE);
SELECT * FROM metadata;
```

Names i,b,t,f; IDs23,20,25,16; INT4,INT8,TEXT,BOOL; native int64(1),int64(2),"x",false; ScanTypes int64,int64,string,bool; Nullable `(false,true),(true,true),(true,true),(true,true)` with known flag second. Origins relate to actual shared table/attributes, not fixed generated IDs. Future `SELECT CAST(NULL AS int8)` retains ID20/INT8 with nil; unknown string output ID25; expression nullability not blanket NOT NULL. Parameters typmod −1, results meaningful typmods/origins even without database/sql exposure; unavailable length/precision returns unknown, not inferred eight bytes.

### W8 — Modifier context and input interpretation

Fresh varchar(5), §8.3: assignment `'too long'`→22001/`value too long for type character varying(5)`; explicit `'too long'::varchar(5)`→`too l`; assignment `'good      '`→`good `; NULL typed. Five `é` fit, six nonspace `é` fail [INFERENCE from character counting], not byte length. Same-type identity still sizes; overload first. PG18-TYPE-CHARACTER owns execution/capture.

Later interval owner, [§8.5.4](https://www.postgresql.org/docs/18/datatype-datetime.html#DATATYPE-INTERVAL-INPUT): `INTERVAL '1' YEAR` is one year, `INTERVAL '1'` one second. The field typmod must influence unknown-constant interpretation; post-input sizing cannot recover it. Paired discriminator:

```sql
SELECT CAST('1' AS interval year);
SELECT CAST(CAST('1' AS text) AS interval year);
```

[INFERENCE from release C, not executed] First: UNKNOWN Const input receives base YEAR typmod, yielding one year (12 months, zero days/time). Second: typed TEXT→INTERVAL CoerceViaIO inputs with -1, yielding one second; subsequent YEAR sizing removes the lower fields, yielding zero (zero months/days/time). Both have INTERVAL identity and destination YEAR typmod; assert logical fields rather than session-dependent display. `parse_coerce.c:coerce_type/coerce_to_target_type`, `execExpr.c` CoerceViaIO initialization and `timestamp.c:AdjustIntervalForTypmod` (§11) independently ground the argument choice and sizing. Future direct/prepared fixtures must preserve the unknown-vs-typed-text distinction; neither case specifies parameter import. This extension witness does not widen four-type TYPE-CORE.

### W9 — Collation/key semantics

```sql
SELECT v FROM (VALUES ('a'),('A')) AS x(v) ORDER BY v COLLATE "C";
SELECT ('A' COLLATE "C") = ('a' COLLATE "C");
SELECT ('a' COLLATE "C") < ('b' COLLATE "POSIX");
```

A before a/false [INFERENCE bytewise C], final 42P21 despite same provider behavior. Internal distinct implicit nondefault collations: insensitive concatenation propagates Indeterminate; runtime sensitive operation 42P22; required common-collation analysis with none_ok=false rejects conflict 42P21; explicit overrides. Check bool result None yet comparison's input collation retained. Provider primary/hint/position require independent capture, not invented ICU/locale availability.

Key units: int4(7)/int8(7) compare zero/equal, hash equally with seed0 and another fixed seed; compatible copies/relabels preserve equivalence. NULL grouping differs from SQL unknown. Equality-only family cannot order/hash; no fallback. Supported nondeterministic equality hashes its equivalence, not original bytes; no PostgreSQL hash-number parity.

### W10 — Ownership/replacement/controls/interleaving

- Import bytes `x`, mutate caller buffer, retained row remains x; mutate exported bytes for supported byte types, retained payload unchanged.
- Hold SELECT rows/metadata through append/Reset/Restore: captured bag/count/metadata/descriptors unchanged, new readers see new state.
- Existing public rebind fixture: prepare `SELECT v FROM p WHERE v=$1` on p(v INT), Reset/replace with p(other TEXT), execute→42703, never stale execution. Cached shape/search-path policy remains PREPARED-REBIND/CATALOG.
- Native round trip integers, newline/apostrophe/backslash text, bool/NULL/empty tables; check deterministic supported dump and logical rows/types. Later bad staged tuple/duplicate/forbidden parameter/reader error/cancel leaves sentinel state; writer error leaves data despite partial stream.
- Blocking reader/writer allows target operations; no target lock across I/O. Final cancellation check prevents publication; ordinary already-blocked I/O is not forcibly canceled by codec contract.
- Concurrent prepared executions have separate frames; descriptor/provider replacement retains old generation; failed bind/coercion/Restore installs no rows/metadata. These are ownership/publication, not isolation/locking algorithms.

## 11. Evidence and verification use

Architectural review traces §1's map, current §8 callers, §9 owners and W1–W10 positive/boundary/failure/interleaving outcomes. `smoke_commands = []` because this document enables no runtime feature; baseline make smoke cannot prove generalized coercion. Implementer ran no SQL/build/test/formatter checks. Root must run fresh full production gates and independent review; report those separately from design expectations. Future consumer owners supply actual public smoke argv and independently specified exact diagnostic fixtures using existing helpers. Docker/image/network failures in enabled reference checks remain failures, not skips/mocks.

Primary references:
- [§10.1 overview](https://www.postgresql.org/docs/18/typeconv-overview.html), [§10.2 operators](https://www.postgresql.org/docs/18/typeconv-oper.html), [§10.3 functions](https://www.postgresql.org/docs/18/typeconv-func.html), [§10.4 storage](https://www.postgresql.org/docs/18/typeconv-query.html), [§10.5 common type](https://www.postgresql.org/docs/18/typeconv-union-case.html), [§10.6 output](https://www.postgresql.org/docs/18/typeconv-select.html).
- [CREATE CAST](https://www.postgresql.org/docs/18/sql-createcast.html), [pg_type](https://www.postgresql.org/docs/18/catalog-pg-type.html), [character](https://www.postgresql.org/docs/18/datatype-character.html), [interval input](https://www.postgresql.org/docs/18/datatype-datetime.html#DATATYPE-INTERVAL-INPUT), [collation](https://www.postgresql.org/docs/18/collation.html), [operator families](https://www.postgresql.org/docs/18/xindex.html#XINDEX-OPFAMILY), [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html), [protocol metadata](https://www.postgresql.org/docs/18/protocol-message-formats.html), [SQLSTATE](https://www.postgresql.org/docs/18/errcodes-appendix.html).
- REL_18_0 C: [parse_coerce.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_coerce.c) `coerce_type` unknown-constant input, `coerce_to_target_type` subsequent sizing, `find_coercion_pathway`, `select_common_type`, `select_common_typmod`; [execExpr.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/executor/execExpr.c) CoerceViaIO target input argument -1; [timestamp.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/timestamp.c) interval input and `AdjustIntervalForTypmod`; [parse_target.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_target.c) `resolveTargetListUnknowns`; [parse_param.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_param.c) `variable_coerce_param_hook`, `check_parameter_resolution_walker`; [parse_collate.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/parser/parse_collate.c) input versus result collation; [pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat) fixed identity/category, regtype=2206 (2202 is regprocedure); [pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat) pg_typeof→regtype.
