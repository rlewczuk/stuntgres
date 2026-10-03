# PG18-ARCH-TYPE-SYSTEM — Typed value and coercion contracts

## 1. Decision, scope, and evidence

This is the decision artifact for the design-only part **PG18-ARCH-TYPE-SYSTEM**. Adopt one proposed `internal/sqltype` seam for resolved type identity, immutable descriptors, nullable runtime values, codecs, and coercion plans. Keep raw syntax in `internal/parser`, binding/evaluation/storage in the existing `internal/engine`, and `database/sql` conversion in root driver adapters. Extend the existing binder, catalog, snapshots, and native controls; do not introduce parallel stores, allocators, binders, or parser/engine type lists.

**The document alone enables no executable consumers. Implementation consumers require PG18-TYPE-CORE directly or transitively.** Interfaces below are future internal commitments, not existing APIs or instructions to implement adjacent features in this part. This artifact neither expands the supported SQL grammar nor implements types, casts, overloads, common-type selection, collation providers, SQL PREPARE, or a wire server. No production code, tests, prerequisite artifacts, or catalog edges are changed by this decision.

### 1.1 Version and diagnostic policy

Normative semantics are the official PostgreSQL `/docs/18/` manual, whose fetched navigation identifies **18.6**. The real reference remains **18.0**, UTF8 encoding and C locale:

```
postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854
```

`reference_helpers_test.go:20,59–60,81–88` pins that image, initialization, and `server_version_num=180000` assertion. Do not replace it with a guessed image. Minor-specific changed behavior belongs to **PG18-REF-ORACLE-18-6**. Local PostgreSQL `../src-ref/postgres/configure.ac:20` identifies **20devel** and is research only. Its `parse_param.c:190–260,290–312` corroborates parameter transitions, not release diagnostics. [REL_18_0 pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat) corroborates fixed builtin identities. Ramsql's string/reflect conversion representation is not reused: it would add another type authority and cannot establish PostgreSQL semantics.

All SQL and unit outcomes in §10 are **independent expectations**, not observations from running this artifact. `[INFERENCE]` marks derived outcomes or English fields requiring independent release capture. No SQL, builds, tests, linters, formatters, or smoke commands were executed while authoring this document. Architectural acceptance reviews the completed artifact; the frozen planner has `smoke_commands = []`.

Future implementation acceptance requires independently expected rows, affected counts, types, metadata, SQLSTATE, English primary/detail/hint, one-based character positions, and conditionally present object fields—not parity alone. Current `referenceStep`/`referenceObserved` (`reference_test.go:17–65`) capture SQLSTATE but not all fields; **PG18-DRIVER-ERROR-FIELDS** owns that expansion. Absent optional fields stay absent. Do not apply blanket table/column annotation to errors that PostgreSQL does not annotate. Ordinary driver argument, Valuer, context, and I/O errors remain ordinary errors at their boundary, not invented SQLSTATEs.

### 1.2 Acceptance map

| Selected requirement | Decision location | Independent witnesses |
|---|---|---|
| Internal values versus driver values and serialization | §2 | W4, W7, W10 |
| Stable identity, descriptors, typmods, NULL/unknown, input/output | §3 | W4–W5, W7–W8 |
| Implicit/assignment/explicit casts and common type | §4 | W1, W3–W5, W8 |
| Function/operator binding and parameter inference | §5 | W1–W3, W6 |
| Equality/order/hash/collation | §6 | W4, W9 |
| Prepared parameter/result metadata | §7 | W6–W7, W10 |
| Ownership and publication/interleavings | §§2–3, §7–§8 | W4, W6, W10 |
| Extension without parallel representations | §§3–6, §9 | W5, W8–W9 |
| Complete clean-cutover callers | §8 | Source inventory and W4, W6–W7, W10 |
| Bounded sessions/backlog and edges not required | §9 | Capability exit witnesses, not registry existence |
| Assignment distinct from operand overload; exact/ambiguous/no-match; metadata | §§4–5, §7 | W1–W3, W6–W7 |

### 1.3 Current seams, not generalized capability claims

The inspected baseline has four resolved parser enum members (`internal/parser/ast.go:6–23`) mixed into column syntax and raw `parser.Value` syntax (`:61–78`). `grammar.y:4–18,50–67` constructs them; `parser.go:35–41` checks signed-int64 integer literals. General expressions/functions are explicitly unsupported (`grammar.y:68–80`).

`internal/engine/engine.go:17–38` stores `[][]driver.Value`, parser columns, and count/type-only `ParameterInfo`. `describe` (`:54–189`) infers from column destinations, uses a `where bool`, rejects mixed-type repeated parameters with 0A000, and checks gaps before allocating. `value.go:12–121` implements primitive input/parameter/literal switches, not a cast registry. `snapshot.go:23–90` exports wire values and compares unchecked Go payload types. `control.go:63–102` independently serializes parser types and primitive values. `conn.go:51–70` retains only prepared count; `stmt.go:11–47` retains AST/count and reuses `conn.execute`; `rows.go:12–60` exposes enum-based database type, ScanType, and known nullability. These are migration seams, not evidence of generalized overload, hash, typmod, or provider support.

## 2. Contract 1 — Syntax, typed runtime storage, and external values

### 2.1 Package and interface boundary

`internal/sqltype` imports neither parser nor engine nor `database/sql/driver`; it uses the standard library and `internal/sqlstate` for SQL error identity. Production stays standard-library/module only. No native ABI or external provider dependency is introduced by naming an extension hook.

Logical Go contracts in `internal/sqltype`:

```go
type ID uint32

type Usage struct {
    Type      *Descriptor
    Typmod    int32
    Collation CollationRef
}

type Value struct {
    descriptor *Descriptor
    null       bool
    integer    int64
    boolean    bool
    text       string
    extension  immutablePayload
}
func (v Value) Type() *Descriptor
func (v Value) IsNull() bool

type Codec interface {
    Input(context.Context, string) (Value, error)
    Output(context.Context, Value) (string, error)
    SQLLiteral(context.Context, Value) (string, error)
}
```

`immutablePayload` is a private extension representation contract, not a public `any` setter. Each owning feature supplies checked constructors/accessors; constructor validation rejects incompatible descriptor/payload pairs. Baseline int4/int8 values use inline signed int64, not a boxed allocation per cell. Boolean and text use their inline slots. Zero `Value` is invalid/unset, not SQL NULL. A constructor for typed NULL requires a defined concrete descriptor; a resolved NULL has identity and no meaningful payload. Accessors must not permit unchecked cross-type assertions.

`Usage` belongs to bound expressions, columns, parameters, and result occurrences. It supplies occurrence-specific typmod/collation; `Value` supplies its descriptor and NULL/payload state. Do not allocate a descriptor for every varchar length or store wire format codes in values. `Typmod=-1` is unspecified; any other encoding and validation belongs to the owning descriptor's modifier provider.

Root-internal adapter contracts (not public APIs):

```go
ImportParameter(ctx context.Context, raw driver.Value,
    target sqltype.Usage) (sqltype.Value, error)
ExportValue(ctx context.Context, v sqltype.Value) (driver.Value, error)
ScanType(d *sqltype.Descriptor) reflect.Type
```

Root owns driver kind dispatch. The inferred parameter descriptor controls interpretation of the input; a Go int64 is not evidence that a SQL expression is int8. DefaultParameterConverter/Valuer processing, argument names/ordinals/count, and ordinary converter/context errors stay at the adapter boundary. Unsupported SQL types/features remain feature errors, not permission to reinterpret unsupported driver input as text.

`Codec.Input` consumes type text input and produces a base typed value. Target sizing occurs subsequently through coercion. `Output` is SQL type text output, not SQL quoting and not driver export. Boolean output is PostgreSQL-style t/f when required by its codec; wire bool remains Go bool. `SQLLiteral` emits a reparsable type-correct expression for native Dump, with escaping/type qualification owned by that feature; central NULL formatting must not call an input codec. A NULL in a typed INSERT destination can serialize as `NULL`; a standalone typed-NULL literal needs its owning type annotation. Extension syntax is enabled only when the owning feature can parse/restore it. Never silently omit an unsupported stored type from a purported complete dump.

### 2.2 Ownership and export

* Detach incoming mutable `[]byte` once before retention (for baseline text, ordinary byte-to-string conversion supplies the immutable copy). Codecs cannot retain borrowed mutable input buffers. Immutable strings and inline scalars can be shared without redundant copies.
* Freeze extension payloads at construction, including element/dimension/member collections. Copying a `Value` must not create a mutation path into another row. Extension accessors expose read-only views or owned copies as their API requires.
* Published descriptors, codec sets, operation handles, and coercion plans are immutable. They contain no cached connection arguments or execution-local state. Rows/cursors retain their descriptor generation across catalog replacement, Reset, and Restore.
* Wire byte output is independently owned relative to retained rows; mutating a returned `[]byte` cannot change stored payloads. No whole-snapshot deep copy is required to achieve this boundary.
* Snapshot projection stays in the existing `engine.Snapshot`. Its future `Values(index int, dst []sqltype.Value)` copies projected typed values into cursor-owned scratch space. `rows.Next` reuses that scratch row and exports each cell into the supplied `[]driver.Value`; it does not rebind types per cell.
* Baseline wire exports: int4/int8 → int64; text → string; bool → bool; any resolved typed NULL → nil. Typed-to-wire conversion cannot erase the metadata descriptor paired with the cursor.

## 3. Contract 2 — Identity, descriptors, typmods, NULL, and unknown

### 3.1 Single catalog-backed authority

A `Descriptor` is an immutable type definition with these logical fields, exposed read-only rather than writable public struct fields:

| Field group | Meaning/owner |
|---|---|
| ID and catalog identity | Nonzero type object ID plus engine/epoch/revision retention from shared catalog authority |
| Namespace/object name | Catalog-supplied namespace/object identity; canonical SQL spelling and driver database type name are distinct |
| Kind/category/preferred | Base/domain/composite/enum/pseudo/range family kind; PostgreSQL category and preferred-target status |
| Defined/shell | Shell identity has name/namespace/ID only; no concrete values until defined |
| Relationships | Domain/base, element/array, and feature-owned composite member links; preserve distinct type identity |
| Default collation | Catalog identity/default provider association, zero/noncollatable where appropriate |
| Codec and modifier hooks | Input/output/literal codec; optional modifier validation, formatting, sizing, length/precision-scale queries |
| Operation hooks | Optional operation-family references, not universal Go comparison/hash behavior |

Do not derive PostgreSQL `pg_type.typlen`, alignment, storage policy, or wire size from Go `sizeof(Value)` or reflect kind. Logical length and numeric precision metadata are independently supplied by the owning type.

A retained registry view exposes:

```go
LookupType(id ID) (*Descriptor, bool)
```

Catalog/name resolution supplies the ID before lookup. The view is a read-only projection of the same engine catalog, not another writable registry or allocator. Absent lookup is handled by the owning SQL name-resolution boundary with its correct diagnostic, never by returning text. ID zero is invalid/unset everywhere; unknown ID 705 is a legitimate analysis placeholder but never a concrete storable user value.

| Builtin | Fixed ID | Canonical baseline SQL spelling | Driver type name | Category/preferred |
|---|---:|---|---|---|
| bool | 16 | BOOLEAN | BOOL | B / yes |
| int8 | 20 | BIGINT | INT8 | N / no |
| int4 | 23 | INTEGER | INT4 | N / no |
| text | 25 | TEXT | TEXT | S / yes |
| unknown | 705 | analysis only | never finalized output | X / no |

Builtin IDs/names stay exact. User IDs come from the shared catalog allocator and are tested for valid identity/relationships, not the PostgreSQL server's generated sequence.

Integration with [PG18-ARCH-CATALOG-IDENTITY](PG18-ARCH-CATALOG-IDENTITY.md), §§2,4,6–7: `sqltype.ID` is the type-object uint32 projection of its OID, not a second identity domain. The type provider uses the same ObjectAddress/ObjectRef, engine ownership, epoch and DefinitionRevision. ID alone does not authorize cross-engine or cross-epoch value/plan use. A descriptor retains its catalog lifetime; a new descriptor revision is published instead of mutating an old one. Engine/catalog adapters implement catalog descriptor access without requiring sqltype to import engine. A retired descriptor remains usable by its captured cursor but is not a current-catalog binding result. Reset/Restore may reuse numeric user IDs in a new epoch; that never validates a stale reference.

### 3.2 Raw syntax and resolution states

Future parser contract:

```go
type TypeName struct {
    Name      string
    Modifiers []Value
    Pos       Position
}
// parser.Column.Type becomes TypeName.
```

Baseline grammar productions emit canonical INTEGER/BIGINT/TEXT/BOOLEAN spellings, with token positions. `parser.Value` remains raw literal syntax with its existing Kind, integer/text/Boolean fields, parameter index, and Position; it is not renamed into a second runtime value. Future expression syntax may wrap it but cannot add a competing resolved type enum. Qualified names and generalized modifier syntax belong to their consumers; this decision adds no grammar.

Modifier binding is engine-owned: validate/evaluate permitted raw modifier constants, hand normalized modifier inputs to the descriptor provider, and retain the returned int32. Providers cannot depend on parser.Value. Formatting/validation/sizing all use that provider's encoding. No consumer guesses `n` directly from a generic int32.

State sequence:

| Transition | Published output | Failure boundary |
|---|---|---|
| SQL → parser AST | Raw syntax and byte offsets, parameter references | Parser error; no execution of a valid prefix |
| AST → literal/parameter analysis | Unknown string/NULL placeholder, initially known primitive literals, unresolved parameters | Analysis-private state only |
| Contextual resolution | Selected descriptor, Usage, immutable coercion/call/key plans | No bound executable description on failed resolution |
| Successful finalization | All executable parameters/results/row usages concrete and defined | Unknown/invalid IDs prohibited before publication |
| Evaluation/coercion | Typed Value, including typed NULL | Failed input/sizing/check/cancellation publishes no row |
| Capture/export/serialization | Paired immutable rows+metadata, driver value or codec SQL expression | Output errors never mutate retained state |

Raw unknown string literals and unresolved NULL literals are binder inputs, not `Value` instances. The former feeds the selected input routine; the latter obtains target identity without input. SQL NULL is a payload state distinct from unknown type. Standalone unknown SELECT/RETURNING output finalizes to text under [§10.6](https://www.postgresql.org/docs/18/typeconv-select.html); contextual INSERT SELECT and set-operation arms defer that default. An unresolved parameter does not default to text merely because an unresolved literal does.

`integerValue` deliberately retains baseline signed-int64 parse bounds through TYPE-CORE; valid baseline integers classify into int4 when in range, otherwise int8 for expression analysis. This is not numeric/decimal literal expansion. A future numeric owner must migrate larger/decimal classification deliberately rather than claiming it from this contract.

Keep `parser.Position.Offset` as source evidence. The diagnostic adapter uses the original SQL to convert a byte offset to PostgreSQL one-based character position; it must not search substrings or count UTF8 bytes as characters. Unknown/absent position is distinct from position one.

### 3.3 Extension invariant

New builtin/user types supply their catalog descriptor, checked value construction, codec, modifier provider if applicable, optional registered casts, and optional operation families through the same view. Catalog validates/stages descriptor relationships and publishes once. Adding a type must not require synchronized parser/engine/rows switches. Arrays retain element identity, dimensions/bounds and element NULLs in an immutable payload; domains retain domain identity/check ownership even where resolution peels to the base; composite members follow catalog column identity. These are extension requirements, not implemented family behavior. Missing codec/provider/defined state is an error, never an identity conversion or guessed `fmt` serialization.

## 4. Contract 3 — Cast eligibility, execution, and common type

### 4.1 Coercion interfaces and eligibility

```go
type CoercionContext uint8
const (
    Implicit CoercionContext = iota
    Assignment
    Explicit
)
type CastMethod uint8
const (
    Identity CastMethod = iota
    BinaryRelabel
    Function
    IO
)
// Immutable CastSpec records Source/Target ID, minimum context,
// method, and catalog/native conversion-provider identity.
PlanCoercion(source Usage, target Usage,
    context CoercionContext) (CoercionPlan, error)
ResolveUnknown(text string, isNull bool, target Usage) (CoercionPlan, error)
func (p CoercionPlan) Apply(ctx context.Context, value Value) (Value, error)
CommonType(inputs []Usage, construct string) (Usage, []CoercionPlan, error)
```

Planning operations are scoped to a retained catalog/type view and its cast providers, not package-global mutable state. A plan retains descriptors, context, method/provider, destination typmod/collation, and dependencies. It is immutable and contains no execution arguments.

| Registered minimum context | Implicit consumer | Assignment consumer | Explicit consumer |
|---|---|---|---|
| Implicit | eligible | eligible | eligible |
| Assignment | ineligible | eligible | eligible |
| Explicit | ineligible | ineligible | eligible |

Eligibility and execution are separate. Identity with no sizing is a no-conversion path, without conversion allocation/call. Equal type ID does **not** eliminate destination typmod enforcement. A binary relabel changes logical type while sharing only an asserted compatible immutable representation; compatibility is directional and never inferred from both values being Go strings/int64. Function conversion runs the real selected provider. IO uses source Codec.Output then target Codec.Input, not `fmt.Sprint`, SQLLiteral, or wire export. Method-specific conversions receive destination typmod and whether the request is explicit; combine conversion/sizing only when the registered provider supports both, otherwise apply sizing afterward.

An unknown plan owns its raw input independently of runtime Value. Its constant materialization phase invokes target input (or constructs target typed NULL without input) before applying remaining sizing/checks; it cannot pass an unknown Value into Apply. Apply on an ordinary typed plan validates its source descriptor/payload contract and returns a value with target identity. Bind-time constant materialization and execution-time parameter materialization use the same codec rules; neither stores mutable raw arguments in a shared plan.

Expression typing precedes destination assignment. Operators/functions admit only Implicit conversions. Assignment's automatic IO to string-category targets or int8→int4 narrowing cannot influence overload candidate selection. Automatic IO from strings is explicit-only, subject to registered/documented overrides ([CREATE CAST](https://www.postgresql.org/docs/18/sql-createcast.html)). Unknown literals are target input, not evidence of an implicit text→every-type edge. No arbitrary graph-transitive shortest path or retrying overloads after an input failure.

### 4.2 Common-type algorithm

The shared `CommonType` extension follows [§10.5](https://www.postgresql.org/docs/18/typeconv-union-case.html) using **ordered** inputs:

1. All identical nonunknown types resolve immediately to that identity, including an exact common domain.
2. Otherwise treat domains as their base types for subsequent selection.
3. All unknown resolves to text. Otherwise ignore unknown during candidate choice.
4. Fail if known inputs have different categories.
5. Start with first known type. Move left-to-right to another type only if candidate→other is implicit and reverse is not. Stop candidate selection upon a preferred type.
6. Require every input, including those not visited after preferred selection, to convert implicitly to the chosen type; produce one ordered plan per input. Failure is not repaired with assignment casts.

CASE provides ELSE first, followed by THEN inputs. Nested UNION/INTERSECT/EXCEPT resolve pairwise, not globally; other documented constructs submit their ordered inputs together. Preserve a domain only in the identical-domain case. Type ID choice is separate from typmod/collation derivation: retain a common typmod only when all relevant inputs establish the same modifier; otherwise use -1. Consumers apply contextual sizing afterward. Collation combination follows §6, not whichever input won type selection. TYPE-CORE does not implement generalized CommonType; **PG18-FUNC-CONDITIONAL** owns its executable use.

### 4.3 Failure classification

| Condition | SQLSTATE/owner |
|---|---|
| Recognized feature not implemented | 0A000, owning SQL feature |
| Supported explicit type pair has no cast | 42846, explicit-cast consumer |
| Assignment mismatch/common-type incompatibility | 42804, storage/construct consumer |
| Callable no match | 42883, callable consumer |
| More than one best callable | 42725, callable consumer |
| Malformed supported type input | 22P02, codec with consumer context |
| Numeric range violation | 22003, conversion/input provider |
| Typmod overlength | 22001, owning modifier provider |

Internal absent capabilities are never successful no-ops. SQLSTATE identity reuses internal/sqlstate; consumer wording/source context and ordinary adapter errors remain separate. Provider failures, cancellation, and failed domain/constraint checks prevent statement publication.

## 5. Contract 4 — Callable signatures and parameter inference

### 5.1 Candidate ownership and selected call

A `Candidate` records catalog callable identity, Function/Operator kind, namespace visibility/rank, ordered formal IDs, result Usage derivation, strictness, default/variadic normalization, and native/routine provider identity. Catalog/name resolution supplies visible candidates and normalized source-to-formal argument mappings. It owns search-path visibility and qualification; authorization owns privileges; routines/native builtins own evaluation frames. Type selection neither searches another catalog nor invents session paths or routine execution.

```go
ResolveOperator(candidates []Candidate, operands []Usage) (BoundCall, error)
ResolveFunction(candidates []Candidate, arguments []Usage) (BoundCall, error)
```

`BoundCall` retains selected callable identity and exact signature, ordered inserted coercion plans, resolved result Usage and catalog dependency identities. Result type follows that signature and supported polymorphic constraints, not Go argument kinds. Unimplemented polymorphic families stay unsupported rather than resolving to arbitrary text. Strict call evaluation yields typed NULL when appropriate; nonstrict provider behavior remains callable-owned.

Normalize candidate defaults/variadics once, using documented visibility rules. Identical visible signatures are deduplicated by path precedence; different signatures compete equally regardless of rank. Within a schema, an effective variadic signature loses to the same nonvariadic signature; indistinguishable default-expanded matches remain ambiguous if no better match exists. Named/default/variadic argument syntax and provider evaluation are not added by this artifact.

Operator entry rules ([§10.2](https://www.postgresql.org/docs/18/typeconv-oper.html)):

* Use an exact signature first. For a binary known/unknown pair, assume unknown has the known type for that exact check; two unknown operands or unary unknown cannot pass this shortcut.
* If the known operand is a domain and the other unknown, next check both domain-base operands.

Function entry rules ([§10.3](https://www.postgresql.org/docs/18/typeconv-func.html)):

* Exact signature first; unknown does not match exactly.
* Then check eligible one-argument function-style type conversion when the name resolves to a type and input is unknown, binary-coercible, or eligible string IO. Composite→string IO is not enabled by functional syntax; explicit CAST/:: remains necessary. This is a cast decision, not admission of assignment casts to ordinary functions.

Both then share the documented best-match filter: discard non-implicitly-convertible candidates (unknown can provisionally match any target); treat domain inputs as base for subsequent steps; maximize exact positions; maximize preferred target positions where conversion is needed; at unknown positions prefer string category if present, otherwise require one agreed category, then preferred type in that category; finally, if all known arguments share a type, try that type at unknown positions and require a unique survivor. Preserve documented filter behavior when no candidates survive a preference test; do not choose registration order. No match is 42883; nonunique best is 42725. Only after selection does unknown input run its selected codec; bad input fails that call rather than changing the selected overload.

### 5.2 Statement-local parameter constraints

Engine binder contracts:

```go
type ParameterInfo struct {
    Count int
    Types []sqltype.Usage // parameter n at index n-1
}
ConstrainParameter(number int, expected sqltype.Usage, sourceOffset int) error
FinalizeParameters() (ParameterInfo, error)
```

Parameter constraint state is private to one statement bind. Preserve sparse tracking until validating all references/gaps; unresolved/gap parameters fail 42P18 before count-sized allocation, invalid references fail 42P02. The first contextual resolution fixes the parameter ID. Published parameter Usage always has Typmod -1 and descriptor default collation, not a destination's length/collation. Every occurrence separately records its applicable conversion/sizing plan.

Different destinations are not automatically inconsistent. An occurrence encountered after the parameter is resolved uses a coercion from that fixed ID; assignment-compatible uses can succeed without changing the parameter description. Distinct unresolved occurrence copies forced to incompatible types before reconciliation fail 42P08. Retain occurrence resolution state/source offsets so that this distinction is representable; an API that only overwrites a map entry on each use is insufficient. Finalization checks every occurrence against the fixed description and publishes nothing on unresolved/inconsistent input.

TYPE-CORE's narrow migration preserves the explicitly unsupported mixed-destination restriction until its owning generalized inference consumer exists; current 0A000 is not claimed to be full PostgreSQL behavior. This document specifies the generalized extension contract and its witnesses, not implementation acceptance of that extension.

Direct and prepared execution share the same binder. Prepared descriptions are independent of execution values. Every execution imports a fresh typed parameter frame against fixed `ParameterInfo`, then applies occurrence plans; no execution mutates shared inference or retains argument buffers in the description. Successful inference checks assert IDs, typmods, collation and inserted occurrence plans, not merely equal result values. Same-input-type retries never re-infer from supplied Go kinds.

## 6. Contract 5 — SQL comparison, key families, and collation

### 6.1 Nullable SQL comparison versus nonnull keys

SQL comparison is a bound callable returning nullable typed Boolean. Baseline strict comparison with NULL yields SQL unknown (a bool-typed NULL); WHERE accepts only true. Do not equate NULL grouping equivalence with SQL `NULL = NULL`.

An immutable operation-family handle binds operand descriptors, provider generation, collation, and independently optional hooks once:

```go
KeyEqual(ctx context.Context, a, b Value) (bool, error)
KeyCompare(ctx context.Context, a, b Value) (int, error)
KeyHash(ctx context.Context, v Value, seed uint64) (uint64, error)
```

These hooks require nonnull compatible inputs. Query/key policy owns grouping NULL equivalence and sort placement. Preserve baseline **ASC NULLS LAST / DESC NULLS FIRST**. SQL comparison overloads and default grouping/ordering families are related only through registered contracts, never inferred from the spelling `=`, `<`, or `>` ([§36.16.5–6](https://www.postgresql.org/docs/18/xindex.html#XINDEX-OPFAMILY)).

Equality availability does not imply ordering or hashing. Consumers requesting absent hooks fail with their own documented SQLSTATE/diagnostic; recognized unsupported functionality is 0A000 until implemented. Never fall back to reflection equality, Go interface comparison, or hash of arbitrary bytes. Family validation/testing must establish:

* Compare sign supplies the family's ordering, and compare-zero agrees with its KeyEqual.
* Equal values hash equally for the same seed, including supported cross-type int4/int8 equality.
* Implicit/binary conversions within a compatible family preserve hash compatibility.
* Collision is not equality; consumers still compare keys.
* Hash bytes need not match PostgreSQL implementation hash bytes.

### 6.2 Collation derivation and provider lifetime

```go
type CollationDerivation uint8
const (
    None CollationDerivation = iota
    Default
    Implicit
    Explicit
    Indeterminate
)
type CollationRef struct {
    ID           uint32 // shared catalog collation identity
    Derivation   CollationDerivation
    SourceOffset int
}
```

These constants describe collation, not coercion contexts with the same names; implementation must namespace/disambiguate the enums. Noncollatable usages carry None. Collatable constants/parameters receive descriptor default collation; column expressions carry column collation. Provider generation retention uses the same catalog lifetime as descriptor/operation handles.

Combination follows [§23.2.1](https://www.postgresql.org/docs/18/collation.html#COLLATION-CONCEPTS): conflicting explicit IDs fail 42P21; agreeing explicit IDs override implicit/default; otherwise a nondefault implicit overrides default, agreeing implicit IDs propagate, and conflicting nondefault implicit IDs become Indeterminate. Indeterminate propagation is not itself an error for collation-insensitive concatenation; a collation-sensitive consumer fails 42P22. Result collation follows the combined inputs when result type is collatable.

C and POSIX remain different catalog identities despite identical byte-order behavior. Missing provider never silently becomes C. Provider registration records truthful provider/version, encoding, locale, determinism, and immutable handles. Comparison/equality/hash must use the same semantics; nondeterministic equality may equate distinct bytes, requiring compatible hashing rather than raw-byte hashing. No ICU execution or provider availability is claimed. OS/ICU integration belongs to the owning feature and cannot violate standard-library-only production policy. This contract specifies no index, transaction, visibility, or lock algorithm.

## 7. Contract 6 — Prepared parameter/result descriptions

Engine metadata contracts:

```go
type ColumnDescriptor struct {
    Name             string
    Usage            sqltype.Usage
    Nullable         bool
    NullabilityKnown bool
    OriginTable      uint32
    OriginAttribute  int16
}
type StatementDescription struct {
    Parameters ParameterInfo
    Results    []ColumnDescriptor
}
func (e *Engine) Describe(stmt parser.Statement) (StatementDescription, error)
// engine.Result.Columns becomes []ColumnDescriptor.
```

Descriptions and their slices are immutable after publication. Origins use catalog table identity and stable attribute numbers; zero means no identifiable physical origin, not an adapter-allocated object. Descriptor/Usage supplies canonical database name and optional logical length/precision-scale queries. Unknown metadata returns `known=false`, never width inferred from Go payload or PostgreSQL physical typlen. Protocol parameter OIDs versus row typmods/origins are semantic evidence ([§54.7](https://www.postgresql.org/docs/18/protocol-message-formats.html#PROTOCOL-MESSAGE-FORMATS-PARAMETERDESCRIPTION)), not a requirement to create a wire server. Format code is transport choice, not Value state.

Baseline physical columns preserve INT4/INT8/TEXT/BOOL, ScanTypes int64/int64/string/bool, and known declaration nullability. Arbitrary expression nullability is unknown unless analysis proves it; nil payload alone does not establish a different type or declaration. Typed NULL retains declared ID and meaningful result typmod. Unknown literal output finalizes to text before Results publication. Add optional RowsColumnTypeLength/PrecisionScale adapters only where the type provides meaningful answers; unsupported values retain absence.

`conn.PrepareContext` retains raw AST and immutable StatementDescription rather than only count; preserve original SQL where source diagnostics and later reanalysis require it. `stmt.NumInput` reads Parameters.Count; statement Exec/Query still route through `conn.execute`. Engine revalidation uses existing describe/execute binding, and result metadata is captured with the row/descriptor generation used for that execution. Per-execution frames are distinct from retained descriptions. A later catalog replacement cannot change metadata halfway through an already returned cursor.

Dependency identities flow to catalog/session owners, including type, cast, callable, collation/provider and referenced objects. [Catalog design §6](PG18-ARCH-CATALOG-IDENTITY.md) owns engine/epoch/revision validity, fixed parameter/result contracts and complete rebind installation; its illustrative `BoundPlan.ResultColumns []parser.Column` consumes this resolved ColumnDescriptor cutover rather than introducing another enum. **PG18-DRIVER-PREPARED / PG18-DRIVER-PREPARED-REBIND** own SQL PREPARE session lifetime, search-path changes, DDL reanalysis and cached result-shape policy. This artifact neither replaces that policy nor creates another prepared binder. TYPE-CORE preserves existing before-execution revalidation while migrating descriptions; a failed bind never executes stale pointers or partly installs new metadata.

## 8. Clean cutover and publication boundaries

This inventory prescribes future edits, **none executed by this design part**. Read anchors again before implementation; line numbers refer to the inspected baseline.

| File/symbols | Required migration and deletion | Retained seam |
|---|---|---|
| `internal/parser/ast.go:6–23,61–78` — Type/SQL/DatabaseType, Column.Type, Value | Delete resolved enum/naming helpers; Column.Type becomes raw TypeName. Keep Value kinds, parameter indices and Position as syntax. | One raw AST, no runtime payload in parser |
| `internal/parser/grammar.y:4–18,50–67`, `parser.go:35–41` | Union typ/type productions emit TypeName; preserve signed bounds/source evidence. Generate `grammar_gen.go` from grammar.y, never edit generated type conventions separately. | Existing lexer/goyacc source of truth |
| `internal/engine/engine.go:17–38` — table, Result, binding, ParameterInfo | Resolved column descriptors; rows `[][]sqltype.Value`; bound usages/coercion/call/key handles; StatementDescription. | Existing engine/catalog/table store |
| `engine.go:45–189` — Describe/describe/bind | Replace column enum checks and `where bool` with descriptor-supported validation, explicit contexts, statement-local constraints. ParameterInfo IDs/Usage, gap preflight retained. | One binder and name/column resolution seam |
| `engine.go:199–279` — prepareArgs, parameterColumn, Execute/resolve | Keep boundary normalization until moved into root adapter; migrate canonical frame to typed parameters. parameterColumn stays diagnostics-only, never a second inference pass. Restore/engine callers use normalized typed input; root owns driver conversion. | Existing direct/prepared execution convergence |
| `internal/engine/value.go:12–121` — input/parameter/literal | Move accepted primitive input rules into owning codecs; delete obsolete helpers and parallel switches after all callers migrate. Replace `where bool` policy with bound plans. Boolean native wire and text codec output remain distinct. | Existing accepted baseline input limits, deliberate diagnostics policy |
| `internal/engine/snapshot.go:11–90` — Snapshot, Values, compare/selectRows | Typed immutable rows/predicates; Values destination becomes `[]sqltype.Value`; delete unchecked primitive compare, use bound operations. | Projection/selection, no whole-snapshot copying; filter/sort after capture |
| `internal/engine/control.go:27–109,128–171` — Dump/Restore | Descriptor type formatting and SQLLiteral replace Type.SQL and nil/int64/bool/string serialization switches. Restore binds/codecs through staging Engine; parameters forbidden. | quoteIdentifier, emit error handling, deterministic native API |
| `conn.go:51–110` — PrepareContext/execute/direct Exec/Query | Consume StatementDescription/shared import and binding path; retain AST/source/descriptions. | parseOne, one engine per exact DSN/Driver |
| `stmt.go:11–47` — stmt/NumInput/Exec/Query | Replace count-only storage with retained description; no separate prepared binder or mutable inference. | atomic closed state, conn.execute |
| `rows.go:12–60` — rows/newRows/Next/metadata | ColumnDescriptor, cursor-owned typed scratch, ExportValue and descriptor adapters. Remove parser import/type switches and default-to-bool behavior. | Context/EOF/Close and existing three metadata methods |
| `internal/engine/engine_test.go:38–43,54–107,109–209` — tuples/Describe/control assertions | Migrate representation-specific helper/ParameterInfo expectations, assert identity and plans as needed. Do not change independent SQL outcomes to fit representation. | Baseline rows/atomicity/snapshot/control witnesses |
| `driver_test.go:93–132,182–220,339–376`; `reference_test.go:17–65,135–170,281–295` | Public metadata/direct-prepared/retained-query/logical type assertions stay independent; extend structured fields through diagnostic owner, not parity/source-wiring counts. | Public API and pinned real-reference anchors |

Future implementation caller search, using the repository search tool over `*.go` and `internal`, must locate:

```
parser\.(Type|Column|Value|Int4|Int8|Text|Bool)|\.DatabaseType\(|\.SQL\(|ParameterInfo|\.Values\(
```

The inspected matches cover rows, engine/control/value, engine_test helpers and inference. Distinguish retained raw parser.Value/Column callers from forbidden resolved enums. Follow parser-internal and generated callers through grammar source as well. A completed cutover has no aliases, re-exports, deprecated conversion helpers, or compatibility shim retaining the old resolved representation. No unrelated diagnostic/lexical behavior is silently widened.

### 8.1 Publication and interleaving invariants

1. **INSERT** (`engine.go:293–324`): stage all tuples, input/coercion/sizing/constraints and cancellation checks; append once only after full success. Failed later tuples or canceled execution publish none of the new rows. CREATE binding/catalog validation similarly precedes publication.
2. **SELECT** (`engine.go:334–343`): capture immutable committed rows, projection and paired descriptor metadata under engine mutex; filter/sort after unlock. Appends do not alter the captured row count or payloads. Retained values do not reference scratch parameter buffers.
3. **Dump** (`control.go:39–55`): capture immutable state before external writes; descriptors/codecs remain retained for serialization. Never hold the target lock across writer I/O. Writer failure can leave a partial output stream but cannot mutate committed data.
4. **Restore** (`control.go:135–171`): read/parse/execute entirely in a staging Engine using the same binder/codecs; reject parameters; install replacement only after all operations and final cancellation check succeed. Reader/input/constraint/cancellation failures preserve target data and catalog state. No target lock over reader I/O.
5. **Replacement**: publish a new descriptor/provider lifetime, never mutate old descriptors. Old cursors stay usable and keep old metadata. New binding uses current catalog ownership; future catalog epoch invalidation is owned by that authority.
6. **Concurrent/repeated executions**: each frame and execution-local scratch is private; immutable prepared descriptions/plans may be shared without argument leakage. Close/cancellation keep existing adapter semantics. Codec hooks cannot cache shared mutable execution state.

These boundaries constrain capture, retention, and atomic publication only. Transactions, MVCC visibility, session attachment, lock scheduling and retries are not specified here.

## 9. Bounded implementation ownership map

These are consumer sessions within existing backlog capabilities, not new catalog records or tasks executed now. The three TYPE-CORE slices form **one** bounded capability; partial completion never marks TYPE-CORE complete.

| Bounded owner/slice | Contract consumed and allowed work | Exit witness / exclusion |
|---|---|---|
| **PG18-TYPE-CORE**, first slice | §§2–3 descriptors/Value/codecs, baseline raw type-name→column binding. Gate: this design plus PG18-DRIVER-ERROR-FIELDS. | Actual typed-column CREATE/INSERT/SELECT for int4/int8/text/bool/NULL/unknown; not registry scaffold. No arrays/new SQL families. |
| **PG18-TYPE-CORE**, dependent slice | §4 assignment/input subset, §5 shared direct/prepared inference through describe/Execute; baseline limits/signed bounds. | W4/W6 positive/error/atomicity. No generalized common type, overload, explicit cast implementation. |
| **PG18-TYPE-CORE**, completion slice | Snapshot/rows metadata/export plus Dump/Restore clean cutover, §8 publication. | W7/W10 retained metadata/native round trip and failure preservation. All affected internal/public callers migrated. |
| **PG18-EXPR-CORE** | Implemented TYPE-CORE plus expression-analysis contracts; one bound/evaluated seam. | Executable expressions with resolved usages; naming interface alone enables nothing. |
| **PG18-CAST-EXPLICIT** | Explicit plans for CAST/::/function-style baseline forms after expression seam. | Explicit success/no-cast errors; W2/W3 extension witnesses. No implicit overload shortcut. |
| **PG18-FUNC-COMPARE-OPS** | Expanded SQL comparisons and cross-width signatures; SQL/key distinction. | W4 comparison width, W9 cross-type compatibility. |
| **PG18-FUNC-CONDITIONAL** | Executable ordered CommonType and inserted conversions. | W5 pairwise/all-unknown/domain/category boundaries. |
| **PG18-FUNC-INFO-TYPE-DESCRIBE** | Identity/typmod formatting and typed NULL metadata. | W1 supplemental type identity; W7 declared NULL type. |
| **PG18-DRIVER-PREPARED**, **PG18-DRIVER-PREPARED-REBIND** | SQL prepared lifecycle, catalog/search-path invalidation, fixed result contract; same ColumnDescriptor and binder. | W6 parameter_types and W10 revalidation/retained cursor. No new type enum. |
| **PG18-ROUTINE-CORE** | Scalar invocation provider and result conversion through typed values. | Exact signature/result identities; routine frames/auth remain their owners. |
| **PG18-FUNC-OPERATOR-USER** | Catalog-visible operator signatures consumed by ResolveOperator. | Exact/ambiguous selection, real selected provider; no second operator catalog. |
| **PG18-CAST-DEFINITION** | Registered conversion edges and actual provider execution. | Context matrix, directional binary/IO checks, not metadata-only success. |
| **PG18-TYPE-ARRAY** | Immutable elements/dimensions/bounds payload and codecs. | Identity/ownership/NULL-element boundaries through one seam; not parser/engine lists. |
| **PG18-TYPE-CHARACTER** | Typmods, padding/truncation, context-sensitive sizing/comparison. | W1/W8; not TYPE-CORE acceptance. |
| **PG18-EXPR-COLLATION** | Provider identity/derivation in bound expressions and key handles. | W9 explicit conflict, indeterminate propagation and provider compatibility. |

**No type-system-design edge is required** for PG18-DRIVER-ERROR-FIELDS, PG18-SYNTAX-IDENTIFIERS, or PG18-TYPE-BOOLEAN-DIAGNOSTICS. They operate on existing diagnostic/lexical/bool-wire seams; they need not wait for executable TYPE-CORE. Catalog/auth/session/routine ownership remains with existing designs/implementations. This map does not edit prerequisite edges/status or extend foundation scope. Generated IDs are tested for relationships, not another server's allocator sequence.

## 10. Independently reproducible architectural witnesses

Run these only in their future owning implementations, with direct/prepared public API fixtures and the bounded real PostgreSQL reference as applicable. Each fixture starts with a fresh case schema/state and checks independent expectations on each backend. Rows without ORDER BY are bags with multiplicity, not ordered lists. SQL shown here is a witness specification, not proof of support in the current baseline.

### W1 — Required assignment versus overload (positive and boundary)

From [§10.4 Example 10.9](https://www.postgresql.org/docs/18/typeconv-query.html#TYPECONV-QUERY):

```sql
CREATE TABLE vv(v character(20));
INSERT INTO vv SELECT 'abc' || 'def';
SELECT v, octet_length(v) FROM vv;
```

Expect affected=1 and one value `abcdef` followed by **fourteen spaces**, with octet_length integer 20. Walk: unknown operands → chosen text/text concatenation → TEXT result → assignment text→bpchar binary relabel → `bpchar` sizing with destination typmod/explicit=false → padded stored value. Destination character(20) cannot influence operand overload. No real conversion function is needed for the binary relabel; sizing still runs.

Supplement:

```sql
SELECT pg_typeof('abc'||'def'), pg_typeof(v) FROM vv;
```

Expect type names `text`, `character` [INFERENCE from documented identity]; these are values of the type-description function, not the SQL result metadata type TEXT of both columns. W1 belongs to expression/string/character consumers, not TYPE-CORE readiness.

### W2 — Exact signature and ambiguous operator (positive/failure)

[§10.2 Examples 10.2–10.3](https://www.postgresql.org/docs/18/typeconv-oper.html#TYPECONV-OPER):

```sql
SELECT text 'abc' || 'def';
SELECT ~ CAST('20' AS int8);
SELECT ~ '20';
```

First: `abcdef`, TEXT; assert selected text/text signature and right unknown input plan. Second: -21, INT8; assert exact int8 unary signature. Third: **42725**, primary `operator is not unique: ~ "unknown"`; complete documented **Hint** `Could not choose a best candidate operator. You might need to add explicit type casts.` The first sentence is not Detail. No selected provider runs on ambiguity. Exact optional fields/position must be captured against 18.0 by the owner, not copied from presentation formatting or local 20devel.

### W3 — Implicit function admission is not assignment (positive/failure)

[§10.3 Example 10.8](https://www.postgresql.org/docs/18/typeconv-func.html#TYPECONV-FUNC):

```sql
SELECT substr('1234',3);
SELECT substr(varchar '1234',3);
SELECT substr(1234,3);
SELECT substr(CAST(1234 AS text),3);
```

First/second/fourth: one `34`, TEXT. First resolves unknown through selected text input; second uses implicit compatible varchar→text relabel. Third: **42883**, primary `function substr(integer, integer) does not exist`; Hint `No function matches the given name and argument types. You might need to add explicit type casts.` Automatic assignment IO integer→text does not admit this function candidate. Binding failure publishes no executable call. These generalized function/cast features are not baseline grammar.

### W4 — Width, input, and atomicity (boundary/failure/positive)

```sql
CREATE TABLE t(i INTEGER);
INSERT INTO t VALUES(1),(2147483648);
SELECT i FROM t WHERE i=2147483648;
INSERT INTO t VALUES('not an integer');
INSERT INTO t VALUES(TRUE);
```

Second fails **22003**, zero rows published. Third returns an empty bag, not assignment overflow: compare actual operand widths rather than narrowing the predicate via column assignment. Malformed string fails **22P02**; Boolean assignment fails **42804**. Expected range primary `integer out of range` and malformed primary `invalid input syntax for type integer: "not an integer"` are [INFERENCE pending release capture]. Independently assert these states plus unchanged bag after each failure, not just equal backend errors.

Signed int4 limits -2147483648/2147483647 and int8 limits -9223372036854775808/9223372036854775807 succeed. Text int8 one beyond each bound fails 22003. A bad later tuple, required NULL (23502), and canceled execution all preserve the pre-statement bag. Context cancellation remains `errors.Is(context.Canceled)` at the native adapter boundary, not fabricated SQLSTATE. Baseline independent anchors: `engine_test.go:83–107`, `reference_test.go:281–295`. TYPE-CORE preserves these limits without implementing decimal numeric literals.

### W5 — Unknown output and ordered common type (positive/failure)

```sql
SELECT 'Hello World';
SELECT text 'a' UNION SELECT 'b';
SELECT 1.2 UNION SELECT 1;
SELECT NULL UNION SELECT NULL UNION SELECT 1;
```

Expect `Hello World` with TEXT metadata; bag {a,b} with TEXT; bag {1.2,1} with NUMERIC; final statement **42804**, primary `UNION types text and integer cannot be matched` ([§10.5](https://www.postgresql.org/docs/18/typeconv-union-case.html), §10.6). Inner NULL pair resolves to TEXT before outer UNION; global flattening is incorrect. Numeric literal support is a future owner, not TYPE-CORE.

Independent binder cases: all unknown→ID25; ordered one-way implicit candidate change; category mismatch; identical domain inputs preserve exact domain ID, domain/base mixture selects base; CASE ELSE is first; same ID but differing modifiers produces typmod -1; final conversion check covers all inputs even after preferred selection. These are unit expectations, not PostgreSQL hash/allocator parity or unordered cast-graph search.

### W6 — Parameter identity and fresh frames (positive/boundary/failure)

```sql
CREATE TABLE p(a INTEGER,b TEXT,c INTEGER);
-- Describe/bind:
INSERT INTO p VALUES($1,$2,$1);
```

Description: Count=2, IDs [23,25], both Typmod=-1; text uses descriptor default collation. Direct and database/sql prepared executions with `(int32(7),"x")` each independently expect affected=1 and row `(int64(7),"x",int64(7))`. In a single retained prepared fixture, next execution `(8,"y")` adds only `(8,"y",8)`; final bag has precisely the two rows, no frame/type leakage.

SQL PREPARE consumer:

```sql
PREPARE p1 AS INSERT INTO p VALUES($1,$2,$1);
SELECT parameter_types FROM pg_prepared_statements WHERE name='p1';
PREPARE bad AS SELECT $1 IS NULL;
```

Expected parameter_types `{integer,text}` independent of later EXECUTE arguments [INFERENCE from PREPARE]. Last statement expects **42P18**, primary `could not determine data type of parameter $1` [INFERENCE pending 18.0 capture]. A gap reference `$2` without resolved `$1` fails 42P18 before parameter-slice allocation; invalid reference fails 42P02.

Independent constraint-unit failure: two distinct unresolved occurrences of `$1`, captured before shared resolution, are forced to int4 and text before reconciliation. Expect **42P08**, primary `inconsistent types deduced for parameter $1`, Detail `integer versus text` [INFERENCE from research, not a captured SQL case]. Contrast an already-resolved int4 parameter then assigned to text: keep ID23, record occurrence assignment conversion, do not overwrite its description or automatically report inconsistency. Current narrow mixed-destination behavior is explicitly unsupported (0A000); this generalized unit contract is not a claim that baseline accepts it. Assert occurrence resolution state and coercion plans in addition to result equality.

### W7 — Public result metadata and typed NULL (positive/boundary)

```sql
CREATE TABLE metadata(i INTEGER NOT NULL,b BIGINT,t TEXT,f BOOLEAN);
INSERT INTO metadata VALUES(1,2,'x',FALSE);
SELECT * FROM metadata;
```

Expect names i,b,t,f; IDs 23,20,25,16; database type names INT4,INT8,TEXT,BOOL; native row `(int64(1),int64(2),"x",false)`; ScanTypes int64,int64,string,bool; Nullable answers `(false,true),(true,true),(true,true),(true,true)` where the second Boolean is known. Anchor `driver_test.go:182–220`. Column origins refer to the actual shared table ID/attributes, never an adapter allocator; compare relationships, not generated OID sequence.

Future expression witness `SELECT CAST(NULL AS int8)` has ID20/INT8 even with wire nil. `SELECT 'Hello World'` has ID25/TEXT, never finalized unknown metadata. Expression nullability is not blanket NOT NULL; meaningful typmod/origin stays in internal metadata even if database/sql has no equivalent field. Parameter descriptions keep Typmod=-1 rather than destination length. Missing length/precision-scale returns unknown, not 8 merely because the Go value is int64.

### W8 — Typmod context and character counting (positive/boundary/failure)

Fresh varchar(5) target, [§8.3](https://www.postgresql.org/docs/18/datatype-character.html#DATATYPE-CHARACTER): assignment `'too long'` fails **22001**, primary `value too long for type character varying(5)`; explicit `'too long'::varchar(5)` yields `too l`; assignment `'good      '` stores `good ` (five characters). NULL remains typed NULL. Five-character `ééééé` fits and six nonspace characters `éééééé` fail assignment [INFERENCE from documented character counting]; test length in characters, not bytes. Same-type input still requires target sizing. Source overload resolution precedes this step. **PG18-TYPE-CHARACTER** owns execution and release diagnostic capture, not TYPE-CORE.

### W9 — Collation and key compatibility (positive/boundary/failure)

```sql
SELECT v FROM (VALUES ('a'),('A')) AS x(v) ORDER BY v COLLATE "C";
SELECT ('A' COLLATE "C") = ('a' COLLATE "C");
SELECT ('a' COLLATE "C") < ('b' COLLATE "POSIX");
```

Expected order A then a and false equality [INFERENCE from bytewise C]. Third fails **42P21**, despite identical provider behavior. For internal derivation units, combine two distinct implicit nondefault collation IDs: concatenation can propagate Indeterminate without error; a sensitive comparison/order consumer fails **42P22**. Explicit override resolves implicit conflict. Actual provider-specific primary/hint/position requires the feature owner's independent 18.0 capture; no invented ICU result or unavailable OS locale is assumed.

Key-unit invariants: int4(7) and int8(7) compare zero/equal under their compatible family and hash equally with seed 0 and another fixed seed; copying/relabeling compatible values preserves the invariant. NULL grouping policy is independent of SQL bool-typed NULL equality. Equality-only family cannot satisfy order/hash request; absent hooks fail rather than fallback. Nondeterministic providers, when actually supported, must hash according to their equality equivalence, not original bytes. Hash numbers are not pinned to PostgreSQL.

### W10 — Ownership, replacement, controls, and interleaving

* Import `[]byte("x")` against text; mutate the caller buffer afterward. Retained row stays `x`. For a feature exporting bytes, mutate exported wire bytes and assert retained payload unchanged.
* Capture SELECT rows+metadata, then append rows, Reset, or successful Restore. Held cursor returns only captured rows with captured metadata/descriptors; new readers see current state. Keep descriptor generation retained, not mutable references into current maps.
* Baseline public fixture: prepare `SELECT v FROM p WHERE v=$1` on p(v INT), replace through Reset with p(other TEXT), execute again. Expect existing **42703** (missing v), never stale execution. `driver_test.go:339–376` independently specifies this. Cached-result-shape changes/search-path invalidation belong to PREPARED-REBIND/catalog contracts, not a new type-system policy.
* Dump/Restore round trip baseline integers, text with newline/apostrophe/backslash, bool, NULL and empty tables; assert logical rows/types and deterministic supported dump. Failed later staged tuple, duplicate definition, forbidden parameter, reader failure or cancellation preserves sentinel data. Writer failure preserves data even if its output is partial. `engine_test.go:152–209` anchors these independent expectations.
* Blocking reader/writer interleaving must allow target operations without waiting on a lock held over external I/O. Cancellation checked before final publication leaves data unchanged; already-blocked ordinary I/O is not forcibly cancelable by a codec contract.
* Concurrent prepared executions have separate parameter frames and no value leakage; changing provider/descriptor definitions publishes replacement generations without modifying a held description/cursor. Failed binding/coercion/restore installs no new rows/catalog metadata.

These are ownership/publication expectations, not transaction isolation schedules or locking algorithms.

## 11. Acceptance and future verification use

Review traces every selected boundary through §§2–7, every affected caller through §8, and every capability owner through §9. W1 explicitly distinguishes destination assignment/sizing from operand-driven overload; W2/W3 specify exact and ambiguous/no-match cases with distinct primary/Hint handling; W6/W7 specify parameter/result metadata. W4/W8/W9 cover boundaries and errors; W10 covers retained state and relevant interleavings. The document is the sole deliverable; the shared internal plan is not its substitute.

Future owners reuse the public direct/prepared fixture and bounded PostgreSQL setup, specifying expected outputs before comparing backends. Capture English primary/detail/hint/character positions and conditionally present object fields against pinned 18.0. Docker/image failures in enabled reference checks remain failures, never skips/mocks. Local 20devel cannot fill that oracle role. Each runtime feature must supply actual public API smoke argv for its new behavior; baseline `make smoke` cannot prove generalized coercion. This design requires no feature commands and reports no runtime verification.

### Normative reference index

* [§10.1 overview](https://www.postgresql.org/docs/18/typeconv-overview.html#TYPECONV-OVERVIEW): unknown, categories, distinct conversion contexts, extensibility.
* [§10.2 operators](https://www.postgresql.org/docs/18/typeconv-oper.html#TYPECONV-OPER), [§10.3 functions](https://www.postgresql.org/docs/18/typeconv-func.html#TYPECONV-FUNC): distinct entry rules and common best-match filter.
* [§10.4 storage](https://www.postgresql.org/docs/18/typeconv-query.html#TYPECONV-QUERY): expression then assignment then sizing, Example 10.9.
* [§10.5 common type](https://www.postgresql.org/docs/18/typeconv-union-case.html#TYPECONV-UNION-CASE), [§10.6 output](https://www.postgresql.org/docs/18/typeconv-select.html#TYPECONV-SELECT): ordered resolution, domains, pairwise sets, final text fallback.
* [CREATE CAST](https://www.postgresql.org/docs/18/sql-createcast.html): eligibility, directional binary compatibility, IO/functions, modifier/explicitness arguments and functional-syntax exception.
* [pg_type §52.64](https://www.postgresql.org/docs/18/catalog-pg-type.html#CATALOG-PG-TYPE), [REL_18_0 pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat): logical descriptor facts and fixed builtin identities.
* [Character §8.3](https://www.postgresql.org/docs/18/datatype-character.html#DATATYPE-CHARACTER), [Collation §23.2](https://www.postgresql.org/docs/18/collation.html#COLLATION-CONCEPTS), [Families §36.16.5–6](https://www.postgresql.org/docs/18/xindex.html#XINDEX-OPFAMILY): modifier semantics, derivation/provider distinction, equality/order/hash compatibility.
* [PREPARE](https://www.postgresql.org/docs/18/sql-prepare.html), [ParameterDescription/RowDescription §54.7](https://www.postgresql.org/docs/18/protocol-message-formats.html#PROTOCOL-MESSAGE-FORMATS-PARAMETERDESCRIPTION), [SQLSTATE Appendix A](https://www.postgresql.org/docs/18/errcodes-appendix.html): contextual inference/lifetime, logical metadata, error identities.
