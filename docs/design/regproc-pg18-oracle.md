# PostgreSQL18 routine-alias evidence

## Scope and authority

REGPROC and REGPROCEDURE are unsigned four-byte function/routine OID aliases, not
execution permissions. This implementation uses complete source-fixed PostgreSQL
18.0 identity metadata beside real installed provider descriptors. It does not add
CREATE FUNCTION/PROCEDURE, arbitrary execution, public pg_proc rows/columns or a
second catalog/binder/store. Release/manual references:

- [PG18 object identifier types](https://www.postgresql.org/docs/18/datatype-oid.html)
- [PG18 pg_proc input argument identities](https://www.postgresql.org/docs/18/catalog-pg-proc.html)
- [PG18 search path](https://www.postgresql.org/docs/18/ddl-schemas.html)
- [PG18 diagnostic fields](https://www.postgresql.org/docs/18/protocol-error-fields.html)
- [REL_18_0 regproc.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/utils/adt/regproc.c)
- [REL_18_0 namespace.c](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/namespace.c)
- [REL_18_0 pg_cast.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_cast.dat)

The manual `/18/` currently identifies18.6; the unchanged runtime oracle is pinned
PostgreSQL18.0 bookworm, UTF8/C, image
`postgres:18.0-bookworm@sha256:3f55f8895c4ed50603e2fbdfc72fffeeaba3173321fee5cb825bbbeb30d9d854`.
Local development source20devel is research only, not release inventory/oracle.

## Observed complete extraction

The root supplied the complete
[REL_18_0 pg_proc.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.dat)
with **654436 bytes**, ending in the `pg_get_aios` identity and closing list bracket.
The implementation extraction consumed all supplied bytes, rather than the remote
reader's truncated prefix. A catalog-data lexer consumed comments, strings,
escapes, row delimiters and list termination; every row required oid, proname and
proargtypes. It validated **3397 identities**, nonzero unique OIDs, unique
`(pg_catalog,name,input-signature)` identities and all **116 argument symbols**.

An independently fetched complete
[REL_18_0 pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat)
(**39283 bytes**) supplied fixed type OIDs and generated arrays from
`array_type_oid`. Every argument symbol/OID was cross-checked against that release
source and the existing complete builtin type inventory. Routine namespace uses
[pg_proc.h](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_proc.h)'s
pg_catalog default. proargtypes already includes IN/INOUT/VARIADIC arguments and
excludes OUT-only arguments; no execution/provider inference is made from prokind.
Extraction was source research, not a build/test/formatter or native verification.

`internal/engine/regproc_identities.go` contains only the resulting fixed identity
facts. Shared name/OID indexes are initialized once; actual descriptors win the
same-OID merge and duplicate conversion identities are not new overloads. No
lookup-only fact is registered as an executable descriptor or projected SQL row.
Runtime-allocated information_schema/plpgsql routines are not assigned invented
fixed identities. They remain an explicit unavailable native catalog capability.

Routine input reuses the environment's captured type authority instead of
recapturing its effective type path. Binder, parameter and COPY assembly capture
that authority once for routine aliases; role authority and the separate
temporary-namespace-excluding function path retain their existing ownership.
This removes duplicate type-path allocation and privilege lookup, not routine
execution or SQL timeout policy.

Exact independent release witnesses include boolin(cstring)=1242, now()=1299,
abs(integer)=1397, abs(bigint)=1396, sum(integer)=2108,
`"RI_FKey_check_ins"()`=1644 and the final pg_get_aios()=6399. lower is overloaded;
lower(text)=870 is not proof of unique-name REGPROC input.

## Observed oracle and native contract

Root Verification1 ran the test-only PostgreSQL scenario and exposed incorrect
inferred expectations. Root Verification2 passed the corrected complete SQL
contract. At that stage the temporary PG-only filter and native-generated stream
deferrals were still present. Root removed them before Verification3, which failed
natively **before production implementation** on missing casts/storage/codecs.
Those observations are ground truth; this implementation agent ran no checks.

Corrected observations retained in the permanent public definitions:

- Both alias families have pq database type names REGPROC/REGPROCEDURE; companion
  arrays report _REGPROC/_REGPROCEDURE. pq ScanType is interface{} and nonnull raw
  transport is []byte. Length/DecimalSize and pq nullability are unavailable.
  Native string transport/ScanType is independently asserted by the completed
  selected native and pinned-reference runs.
- Alias frames redact values: Context `unnamed portal parameter $1 = '...'`.
  Empty inputs retain the existing empty-parameter convention. Runtime TEXT IO
  has no portal Context; SQL literal errors use their outer Unicode-character
  source coordinate.
- `abs(integer(4))` fails42601 near `(` with decoded type Position8 and Context
  `invalid type name "integer(4)"`; `abs(NONE)` fails42601 near NONE with decoded
  Position1 and Context `invalid type name "NONE"`. Literal routes replace those
  positions with the outer literal coordinate (17 in the UTF8-prefix witness).
  Frames append newline plus the redacted portal Context. numeric(0) fails22023
  through modifier validation, without invented syntax Context.
- json_object(text[2][3]) resolves3202 and formats `"json_object"(text[])` through
  the canonical identifier keyword owner. Bool-to-alias failures are42846 at12;
  TEXT assignment rejection is42804 at41 with the standard rewrite/cast Hint.
- A relevant held-literal path invalidation reports Position8. Late-row atomic
  INSERT failure reports61 and publishes no partial rows. Dynamic stored constant
  dependencies use the exact existing default/view Detail and CASCADE Hint.

Verification8 passed selected native and pinned PostgreSQL checks for the original
eight REGPROC definitions and `TestReferenceRegtypeAliasModifierSyntax`, including
actual emitted-Dump replay, retained output and displaced-engine remapping, after
the captured type-authority reuse correction. Its fresh quickcheck and public
API smoke also passed. These observations cover only those exercised scenarios;
the two review regressions now additionally pass completed native/pinned checks.
Numeric/dash argument syntax is rejected across all six routes, while whole-input
OID aliases retain numeric admission. Mixed routine/REGROLE composite rows,
defaults and views survive displaced-role Restore and actual PostgreSQL replay;
new default-driven INSERTs retain receiving-catalog role identity.
Verification6's synchronized candidate passed `make quickcheck`; the subsequent
documentation-state quickcheck failed in `TestReferenceDatetimeSleepFeatureSmoke`
with an unexpected statement timeout at `datetime_sleep_owners_test.go:19`.
That helper runs several ordinary statements under a configured 100ms timeout;
the failure log does not identify which statement expired or its scheduling
delay. Causality from routine-input capture is unproven, and a load/scheduling
explanation is **[INFERENCE]**, not an observed diagnosis. Verification8 confirms
the scoped capture-reuse correction on its selected paths; datetime
behavior/timeouts are unchanged. A standalone public
Go program observed INT4/REGPROC/REGPROCEDURE metadata, boolin and quoted RI
symbolic rows, missing-signature SQLSTATE42883, held recovery to OID1397, and
identical rows after actual Dump/Reset/Restore. Post-integration finalcheck and
independent review remain separate workflow gates.

## Ownership and compatibility invariants

Input name syntax reuses the decoded qualified-name parser. Signature argument
scanning respects quoted/doubled identifiers and nested modifiers/array brackets,
resolves each argument through the captured decoded SQL type-name resolver and
caps at100 arguments. This private resolver shares REGTYPE's parser/lookup/modifier
tail, not its whole-input numeric/dash shortcuts; it checks cancellation on entry.
Earlier argument type errors precede later signature errors. Lookup masks only
identical signatures across the entire effective function path and excludes temp
routine namespaces; qualification checks actual schema authority. Only whole
REGTYPE/REGPROC/REGPROCEDURE inputs admit pure digits and exact `-` through the
existing OID numeric codec. Numeric/dash signature arguments instead receive
type-parser diagnostics, with no argument-string special cases.

The approved shared decoded-type correction uses the canonical keyword owner's
type/function-name category for generic type names. It is not a NONE special
case: specialized builtin type syntax, quoted/qualified names and ordinary SQL
annotation grammar retain their existing owners. The independently observed
REGPROCEDURE six-route NONE regression exercises that shared type-input seam.
Private parser units retain specialized/qualified/ordinary grammar witnesses.
`TestReferenceRegtypeAliasModifierSyntax` additionally retains its original
modifier failures and adds standalone REGTYPE six-route NONE records plus
valid quoted/qualified domain and specialized builtin witnesses. It reuses the
independent REGTYPE diagnostic owner, not the routine-alias error helper. The
completed selected pinned-oracle run independently confirms these new records.

Output canonicalizes argument type names, quotes identifiers and captures all
presentations before returning rows. Prepared constants share the existing
catalog-identifier analysis cell, keyed by source occurrence and alias type;
referent-only drop does not retarget them. Runtime/parameter input remains fresh.
No new all-DDL invalidation, live Rows catalog lookup or provider admission exists.

Dump serializes scalar aliases with qualified REGPROCEDURE signature identity and
an explicit REGPROC cast when needed. Recursive container fragments resolve the
same signature and emit its numeric identity at replay, preserving lower bounds,
NULLs and nested quoting without relying on an overloaded REGPROC name. Existing
restore address maps remap actual ProcedureClassID identities recursively through
arrays/composites/domains and stored expressions. Fixed identities remain fixed;
unknown numeric values are not invented remap keys. Data values do not own their
referents; stored expression constants depend on real provider descriptors.

Mixed routine/REGROLE composite serialization must retain the existing context
role formatter for non-routine leaves, so names resolve against the receiving
role catalog rather than preserving backend-local source OIDs. The new
`TestReferenceRegProcMixedRolePersistence` targets the supported composite shape,
row/default/view output, displaced native Restore and actual emitted-stream replay.
Native arrays of composites and scalar REGROLE stored constants remain unsupported.

Restored container DEFAULTs can retain character-to-container coercions rather
than a pre-evaluated datum. Their consuming write captures recursive catalog
input through the existing `captureRetainedWriteInput` / `captureElementInput`
owners, sharing role, type and routine authority without rebinding the stored
expression. Existing diagnostic naming does not suppress independent catalog
input demands. Binding publication includes late view-write captures, and COPY
publishes retained input before assembling its explicit destination inputs.
The private `TestRoutineRetainedWriteInputAuthority` evaluates real mixed
routine/role/type composite coercions on default/generation/check routes with
and without preexisting diagnostic naming; public persistence remains the
consumer-visible proof, including INSERT through the restored DEFAULT.

The six-route `TestReferenceRegProcSignatureTypeSyntax` independently targets
numeric/dash argument rejection, held valid recovery and whole-input shortcuts.
Root Verification 12 records completed native/pinned proof for this definition
and `TestReferenceRegProcMixedRolePersistence`, including restored-default INSERT
and actual emitted-stream replay. Historical eight-definition passes alone did
not establish those fixes.

Review 2 identified a separate mixed routine/operator capture coverage gap, not
an observed production defect. `TestRoutineMixedOperatorCompoundCapture` uses a
real published operator descriptor and a supported routine/operator/type
composite to check exact ordered fresh operand stamps and dependencies, retained
replay at the same occurrence, shared-cell classification, actual operand revision
invalidation/recapture, and qualified operand-authority denial/recovery.
`TestReferenceRegProcMixedOperatorPersistence` specifies builtin identities
1397/551/23, canonical mixed text, direct/held string/byte/NULL frames with alias
metadata and member identities, explicit row/default/view replay, restored-default
evaluation and unread retained native output/metadata across Reset/Restore.
It replays actual native Dump bytes on PostgreSQL without adding operator SQL
functionality or composite arrays. Completed root native and pinned-reference
checks now confirm the public scenario; the private lifecycle selector also passes.

The first root-owned R4 execution reached native Reset/Restore and then failed
the fresh restored query with `routine alias input lacks catalog authority`
(artifact157); the private selector separately exposed an ObjectAddress/OID
compile mismatch (artifact159). Root's traced public probe then isolated the
restored view failure: join-legality planning reaches `prepareStoredExecutionRoot`
before `prepareStoredQueryLocked`, with no routine resolver in its input.
The serializer emits concatenated TEXT with nested routine-to-OID-to-TEXT
conversions and an outer composite cast; these remain executable in the frozen
view rather than becoming one stored datum. The late source-opening capture
therefore could not fix the earlier planning failure and has been removed.
Source admission now walks retained view expressions and nested stored sources
through the existing binder's coherent catalog authority, before its complete
input environment is published. Reads and writes share
`captureRetainedExpressionInput`, `captureElementInput` and diagnostic-input
owners, retaining all catalog alias/ACL/naming authority without evaluating
branches, rebinding frozen identities, acquiring relation locks or adding
live-resolution fallbacks. Completed root native/pinned checks confirm this
remedy, including restored view queries and new default-driven INSERTs.
The private qualified-namespace assertion compares the actual namespace
ObjectAddress directly. The oracle-grounded composite quoting expectations
remain unchanged.

Review 3 identified a distinct direct frozen consumer: materialized refresh
does not pass through ordinary view binder admission. Population now invokes
the same retained-definition/expression walker before its first stored-query
preflight, using the population owner's coherent authorization and restricted
`pg_catalog, pg_temp` path. It preserves operation presentation settings,
recaptures demanded catalog input owners into a local candidate, publishes only
after successful capture, and restores input, identity and principal on every
exit. No frozen identity rebind, branch evaluation or live resolver fallback is
introduced. Ordinary source opening and recursive join-legality preparation
consume the input published by their enclosing binder or population owner.

Verification 16 records oracle-only SQL discovery success, an invalid initial
native fixture role-recreation phase (Reset preserves roles), and the corrected
native Restore failure `type "mixed" does not exist`. The materialized dump
renderer used nil diagnostic visibility, emitting an unqualified outer
composite cast even though the inner routine-container cast was qualified.
Dump now supplies an explicit empty visibility path to its existing canonical
renderer, qualifying user types for restricted-path replay. Snapshot import's
closed scalar validator also admits recursively validated TEXT concatenation,
which the existing routine-container serializer emits; no arbitrary function
or definition execution is admitted.

The next observed native Restore failure was `materialized view snapshot
coercion changes persisted datum`: the exact-snapshot type policy rejected
the serializer's qualified REGPROCEDURE-to-OID physical retag, then would
reject its OID-to-TEXT decimal codec. Both steps preserve the unsigned identity;
symbolic source spelling is presentation, not the persisted datum. The policy
now admits only shared uint32 OID/alias retags and canonical OID-to-character
output alongside existing literal codecs. Integer-to-OID wrapping,
OID-to-signed-integer conversion and other semantic conversions remain rejected.
The existing unsized-versus-sized datum comparison still rejects truncation,
padding and rounding, including nested modifiers. No catalog-containing-type
exemption, disabled ExactSnapshot policy or snapshot whitelist widening is
involved. Private `TestSnapshotOIDIdentityCodecLossless` checks zero, fixed and
high-bit/max-uint32 identities across the existing alias carriers, exact decimal
round trips and lossy signed/wrapping/character-width boundaries. Existing
lossless temporal/numeric/container guards and recursively validated closed
concatenation remain intact; the selected private lossless boundary tests pass.

`TestReferenceRegProcMaterializedPersistence` retains the nonpublic mixed
routine/operator/type/role composite and direct plus nested frozen consumers.
Native controls must restore saved stale population 1, then refresh to source
2 and subsequently 2/3. Actual emitted SQL creates PostgreSQL materialized
views WITH NO DATA: exact 55000 precedes explicit refresh to 2, then 2/3.
`TestMaterializedRefreshOwnerContext` additionally exercises a real retained
TEXT-to-REGPROCEDURE coercion and caller-versus-population input capture and
restoration on success and producer failure. Completed native and pinned-reference
proof now confirms R5, its actual emitted stream replay and selected preservation
units. A standalone public API smoke independently observes saved population1
and refreshed population2 with correct routine/operator/type/role output for both
direct and nested materialized definitions. Prior Verification15 remains the
historical proof of earlier consumers, not evidence for these later corrections.

The public authority fixture intentionally uses PostgreSQL schema USAGE grants
versus supported native schema ownership transfer. Native schema GRANT/REVOKE SQL
is not added; bootstrap-owned tables are not transferred. Native controls are
native API phases only. PostgreSQL checks ordinary SQL replay of actual emitted
bytes, not a simulated Dump/Reset/Restore interface. Native ABI/shared-library
loading, external procedural runtimes/process execution, wire-service expansion
and PostgreSQL physical persistence/durability are not supplied by alias lookup.

Permanent test selectors and smoke argv belong in
[reference/type_resolution Testing](../../reference/type_resolution/README.md#testing).
