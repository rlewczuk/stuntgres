# query_outer — query sources and outer composition

Public database/sql reference scenarios cover outer-join composition, metadata,
restriction placement, diagnostics, persistence and source lifecycle. SQL routine
result sources share the same query composition and fixture entry points.
Polymorphic routine outputs reuse these owners for declaration-preserving OUT,
INOUT, TABLE and SETOF specialization, including duplicate rows, typed NULLs,
held argument frames and native emitted-script replay.

`JSON_TABLE` relational FROM sources belong here, including nested rows,
PASSING, path/policy authority, diagnostics, stored definitions, cancellation and
native replay. Scalar SQL/JSON query functions remain in
[binary_json_arrays](../binary_json_arrays/README.md#sqljson-query-functions);
JSONPath evaluation is in [datetime_formatting](../datetime_formatting/README.md#testing).

## Testing

Follow [the shared testing policy](../../docs/TESTING.md). `main_test.go` owns
`fixture.Main`; scenarios use `fixture.NewScenario` and one shared backend body.

`resultsScenario` borrows one connection per backend. `resultsBoth` checks direct
and fresh prepared execution; `resultsHeld` exercises one real held statement.
`resultsMetadata` checks complete independently specified descriptors before any
row is read, including empty sets. `resultsError` checks the complete diagnostic
and prepare/execute boundary. Local native replay helpers call the public controls
on the fixture's unwrapped native pool; emitted SQL is replayed unchanged on the
oracle, without a second fixture implementation or cross-package helper imports.
`resultsBothMetadata` supplies explicit per-column native adapter profiles for
catalog casts, stored catalog fields and null-extended lateral outputs.
`resultsCountBoth` checks COUNT's known-nonnullable native result. These profiles
do not change pq expectations or omit any metadata accessor.

Attribute scenarios use `attributesSettingBoth` for direct setting-call TEXT
results, with explicit known-nullable native metadata and unchanged pq unknown
nullability. `attributesTextBoth` keeps computed nullability unknown for wrappers,
identity casts and user-routine results; profiles are selected by the caller, not
by inspecting SQL text.
`TestReferenceRoutineAttributesStrictPlanExecuteDemand` distinguishes planner-known
literal/custom-bound NULL elimination from generic parameter invocation demand for
both SQL and PL/pgSQL routines. Direct and same-held consumers check live EXECUTE
revocation/regrant, exact admission diagnostics and genuine body effects. Run its
anchored selector with `REFERENCE=1` first, then native.

Routine formal-catalog scenarios read the complete represented non-IN builtin
roster and explicit all-IN counterexamples through direct and prepared queries.
Declaration expectations come from released catalog/contrib sources. Installed
owners, canonical aggregate signatures and generated constructor relationships
reuse the native replay helpers; generated identities are checked within each
owner, not pinned to another backend's allocation sequence. Raw formal-array
checks retain oid[] and internal "char"[] metadata even for NULL values.
The complete represented builtin `pg_proc` inventory includes all twelve
statistics I/O declarations, including non-callable INTERNAL receive metadata,
and all generic-file read/binary/stat/tmpdir overloads. Twelve metadata-only RI
trigger functions retain their released volatile classification and defaults.
Release-owned TSV facts preserve exact membership and all 28 projected fields,
including tmpdir's explicit cost 10 and row estimate 20.
The nine database/relation size overloads retain their released strict volatile
metadata; the one-argument SQL wrapper has a declaration body and retained
released cost 1, as do the internal implementations.
The four zero-input session-activity declarations retain their exact released
identities, result types, strictness, volatility and parallel-safety metadata.
The zero-input trigger-depth declaration retains OID3163, INT4 result and its
released strict, STABLE, parallel-restricted internal-function metadata.
The two rewrite inquiries retain their fixed zero-input STABLE, strict,
parallel-restricted scalar identities; only OID4566 declares a named OUT `oid`.
Settings flags and option expansion retain released STABLE/strict declarations,
unnamed versus fixed input/OUT formal metadata, and the option row estimate 3.
The eight legacy transaction inquiries retain distinct bigint/txid_snapshot
signatures, their modern implementation symbols, strictness, volatility and
parallel-safety declarations; snapshot member expansion has row estimate 50.
The four zero-input control-data functions retain scalar RECORD results and
their exact ordered OUT types/names (18/4/5/12 members for
checkpoint/system/recovery/init), without inventing input or set-returning slots.
The two input-validity declarations add released OIDs6210/6211 with exact
strict/STABLE/parallel-safe scalar metadata. Only error-info has named inputs
and four TEXT OUT fields; the boolean formals are unnamed. Membership and all
28 direct/prepared property assertions include both records.
The four function/operator/opclass/opfamily visibility inquiries retain released
OIDs 2081/2082/2083/3829, strict STABLE parallel-safe OID-to-BOOL signatures and
cost 10 in the exact inventory and all projected metadata fields.
The physical compression inquiry retains OID2121, its one-any strict/STABLE/
parallel-safe TEXT signature, cost 1 and internal implementation symbol.
Use `RUN='^TestReferencePGProcBuiltinInventory$'` in both modes.
The rule and trigger definition functions add four independently released
`REL_18_6` overload records (1573, 1662, 2504, 2730), including their strict,
stable, parallel-safe defaults and internal implementation names. Membership
and complete direct/prepared property batches migrate together.
The two one-OID partition inquiries retain released records 3352/3408, including
strict/STABLE/parallel-safe scalar TEXT metadata and unnamed input formals.
The four one-OID routine declaration inquiries add independent released records
2098/2162/2165/2232. `TestReferenceRoutineDeparse` covers typed signatures,
defaults versus identity arguments, modes/results, builtin/aggregate distinctions,
public diagnostics, captured presentation and native-control replay. Select each
changed definition in both native and pinned real PostgreSQL modes; the
`FeatureSmoke` definition exercises returned DDL and native replay end to end.
`StandardBodies` checks all sixty represented builtin/installed SQL-standard
definitions against independent pinned PostgreSQL strings under both identifier
policies, then exercises installed rename, path masking and native fresh-driver
remapping. Inquiries never execute the body, including unavailable native math.
`InternalMetadata` checks all four inquiries for the twenty-six parser, vector
I/O, DDL-receiver and RI declarations formerly exposed only by pg_proc row
supplements. The complete released inventory contains 2104 rows, all backed
by actual descriptors; metadata ownership does not grant execution. XML aggregate
OID2901 has one unnamed XML formal, an XML result, non-strict IMMUTABLE/
parallel-safe metadata and `aggregate_dummy` implementation identity.
Builtin routine deparsing presents its XML arguments/result and rejects its
function definition as an aggregate. The XML
predicate records independently pin OIDs 2614/3051/3052/3053, strict scalar BOOL
results with TEXT/XML input signatures, internal symbols and parallel-safe metadata.
Only the unsuffixed validator is STABLE; the remaining three are IMMUTABLE. The encoding
conversion and keyword routines retain independent release facts for OIDs
1264/1597/1686, including the keyword OUT shape, cost 10 and row estimate 500.
`TemporarySources` checks masked own-session TEMP qualification against exact
pinned definitions, with direct, fresh-prepared and held calls under both
identifier policies on the same borrowed physical connection.

Standalone procedure scenarios use `proceduresScenario`, `proceduresBoth` and
`proceduresHeld` with declaration-owned output metadata checked before iteration.
CALL TEXT descriptors use `resultsTextMetadata`'s unbounded length for both
adapters; sequence state uses the existing known-nonnullable native profile.
`proceduresDefaultCountsCheck` checks bounded canonical declaration defaults
through direct/prepared queries and the same native replay phases.
Their SQL and PL bodies modify real rows; native control phases retain two
roundtrips, independent Driver ownership and unchanged emitted-SQL oracle replay.
PL CALL writable-destination copyback is not part of these standalone consumers.
Quoted SQL output-shape replay checks separate stored declarations from reached
function or CALL execution and same-held recovery. Checked bodies still reject
invalid shapes during loading. Native Restore DEFAULT INSERTs reach function
errors atomically; top-level CALL retains its separate Restore admission boundary.

```sh
make check RUN='^(TestReferenceRoutineResults|TestReferencePseudoTypes(Result|Record))'
make check RUN='^(TestReferenceRoutineResults|TestReferencePseudoTypes(Result|Record))' REFERENCE=1
make check RUN='^TestReferenceRoutineProcedures'
make check RUN='^TestReferenceRoutineProcedures' REFERENCE=1
make check RUN='^TestReferenceQueryOuterJoin'
make check RUN='^TestReferenceQueryOuterJoin' REFERENCE=1
```
- `JSON_TABLE` relational sources: `RUN='^TestReferenceJSONTable'`

JSON_TABLE helpers stay in `json_table_helpers_test.go`; shared setup/runners and
native-control replay use `fixture.JSONTableScenario`, `SQLJSONBoth`,
`SQLJSONDiagnostic`, `SQLJSONQueryOne` and `JSONPathNavigateReplay` with its
single-owner replay/metadata closure. Cancellation uses the existing provider
admission helpers; retained native controls replay actual emitted SQL on the oracle.
