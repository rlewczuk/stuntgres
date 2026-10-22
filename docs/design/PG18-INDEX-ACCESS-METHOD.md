# Registered access-method definitions

## Contract and authority

`CREATE ACCESS METHOD name TYPE INDEX|TABLE HANDLER [schema.]function` and
`DROP ACCESS METHOD [IF EXISTS] name [RESTRICT|CASCADE]` use one global catalog
name and the ordinary immutable descriptor/provider/candidate/publication owner.
Access methods are class2601 objects, not schema members, owned objects or ACL
containers. `catalog.Read` resolves and enumerates the current descriptors;
compiled bootstrap facts are not a second runtime name registry.

Bootstrap heap2/handler3, btree403/330, hash405/331 and gist783/332 remain pinned.
User method identity is allocated normally and is independent of its selected
physical index implementation. There is no inherited builtin default class:
a custom btree method requires an actual class whose method identity is custom.
Existing OPCLASS/FAMILY DDL, default/name keys, member ownership and support
binding are consumed, not replaced or implemented a second time.

## Genuine native capabilities and TABLE boundary

Four real installed Procedure descriptors associate released handler identities
with finite native operation selectors. bthandler selects the existing logical
B-tree root/build/maintenance authority; hashhandler and gisthandler retain the
existing hash/GiST authorities. heap_tableam_handler selects the existing
ordinary heap storage/visibility authority. Heap is a distinct selector, never
the old BtreeMethod placeholder or a manufactured index routine pointer.
Procedure validation checks the exact registered identity, namespace, internal
argument, result, function kind, volatility/strictness and exclusive capability.
Lookup-only released signature/result metadata cannot grant an implementation.
The released handler metadata is volatile (`v`), strict and parallel-safe (`s`)
for heap, B-tree, hash and GiST; exact native identities preserve those facts.

`visibility.ResolveHeapAccess` resolves the captured method and genuine installed
handler3 through the ordinary descriptor/provider contract, then selects concrete
zero-state `visibility.HeapOperations`. That type owns the single existing
`LiveVersion` body over actual storage versions, end roots, frontier and private
overlays. The default ordinary-heap `visibility.LiveVersion` API intentionally
uses the same body; it is neither a second implementation nor a deprecated shim.
Ordinary captured sources, inheritance/reselection and replacement bind builtin
heap2/handler3 once and explicitly pass operations into `exec.SelectQuerySource`.
There is no nil-View fallback, per-row catalog lookup, callback/interface
dispatch or extra storage type. Custom TABLE definitions resolve the same actual
authority without changing ordinary tables' method identity or selection syntax.

TYPE TABLE supports definition, catalog, dump, restore and drop. It does not add
`CREATE TABLE USING`, `ALTER TABLE SET ACCESS METHOD`, a table-method GUC, custom
TABLE DML or a new storage abstraction. Existing ordinary tables still use heap2.
No handler is made scalar-callable by manufacturing a PostgreSQL internal return
datum. Arbitrary PostgreSQL shared-library/C-ABI loading remains external.

A correctly typed but unregistered native handler returns native0A000
`access method handler has no registered native implementation`; PostgreSQL can
accept the corresponding real server handler. A TABLE method used for an index
returns native55000 before any heap operation is interpreted as index mechanics.
Pinned PostgreSQL18.6 instead reports XX000
`index access method handler function 3 did not return an IndexAmRoutine struct`.
The shared test declares this divergence explicitly, not as parity or a native
handler invocation.

## Admission and operation identity

Prepare parses and describes utilities without effects. Execute shares existing
read-only and explicit-transaction policy. CREATE checks superuser first, then
global collision, exact function-only `(internal)` lookup, exact return kind and
registered capability. There is no handler call probe or independent EXECUTE
privilege check. DROP resolves existence/IF EXISTS before existing-object
superuser, pinning and the shared dependency closure. One ordinary catalog stage
publishes the final descriptor/name/retirement effects. Publication fences outgoing
method/handler reads and incoming consumers against concurrent utility changes.

Index definitions retain MethodOID separately from their physical Method enum.
CREATE resolves a live method once and passes that descriptor through attribute
binding. Family/class strategy, support, storage and signature validation dispatch
on the selected implementation while preserving custom identity in every key and
member descriptor. The genuine support binder captures actual member/procedure
revisions; it never consults names per datum or falls back to builtin defaults.
Snapshot retargeting and semantic comparison include exact method identity;
replacement uses the existing typed destination mapping when identities move.

Capability validation dispatches on the physical enum but formats messages with
the exact declared method name, including hash and GiST ordering options.
Classless custom GiST ordering keeps the existing native early0A000 capability
boundary; pinned PostgreSQL18.6 first reports42704 for the missing integer
default class with the standard default-class Hint. The shared regression
independently asserts both, rather than inventing a GiST/integer class.
Exclusion admission resolves registered descriptors before unsupported builtin
spelling boundaries: a genuine custom btree method named gin/brin is btree;
only unresolved native names retain the prior unsupported-exclusion diagnostic.

## Catalogs and inquiries

`pg_am` exposes the four PostgreSQL columns with their actual OID/NAME/REGPROC/
internal-CHAR types, using current captured descriptors and independent owned row
storage. `pg_class.relam` uses the declared index MethodOID. Object-address,
identity and dependency descriptions resolve the same live authority. Custom
method identifiers are quoted in family/class/index replay text.

`pg_indexam_has_property` demands a catalog snapshot and routes through the
existing inquiry runtime. The selected implementation supplies capabilities;
missing methods, TABLE methods and unknown properties return NULL. Native
2742/4000/3580 are absent from pg_am and therefore return five NULL capabilities.
The independent PostgreSQL vectors remain the released GIN/SP-GiST/BRIN values;
this consistency correction does not advertise unsupported native methods.
All existing btree/hash/GiST vectors remain unchanged. Held new executions
recapture; already captured rows retain their own values and metadata.

## Dependencies, persistence and replacement

Family to method is AUTO, class to family is AUTO, and index to class is NORMAL.
Required members retain their original INTERNAL class ownership. Empty family/
class trees drop without CASCADE or NOTICE. A real index blocks RESTRICT with
index-to-class detail. CASCADE reports the actual normal index consumer, retires
the full automatic/internal graph and leaves the parent table/data intact.
Method to handler is logically NORMAL; materialized references to pinned stock
handler procedures are suppressed by the existing pg_depend pinning rule.

Dump uses the existing captured declaration graph, not a new phase or registry.
Each nonbuiltin method emits exactly one quoted CREATE with qualified handler and
no owner statement, before dependent families/classes/indexes. Restore admits the
same utility and resolves destination capabilities normally. The existing global
identity/TEMP collision mapper remaps method descriptors, family/class method
references, SQLIndex MethodOID and class identities. Source support captures are
not transplanted: destination validation/root reconstruction binds actual current
members and procedures. Existing atomic replacement, cancellation, late-failure,
retained TEMP and eager-row ownership rules remain authoritative.

## Proof and public smoke

The seven shared `TestReferenceAccessMethod` definitions live in reference/indexes:
Definitions, Diagnostics, QuotedDiagnostics, BtreeSmoke, Dependencies, Controls and Capabilities.
Initial pinned PG discovery and native RED were root-observed before production,
including the later review regressions. Final remedy verification is root-owned;
this document does not claim it passed.
Controls retain actual native-emitted-byte PostgreSQL replay, not reconstructed
SQL pretending to be a dump.

Selected proof, each under an external900-second deadline:

```sh
make check RUN='^TestReferenceAccessMethod(Definitions|Diagnostics|QuotedDiagnostics)$'
make check RUN='^TestReferenceAccessMethod(Definitions|Diagnostics|QuotedDiagnostics)$' REFERENCE=1
make check RUN='^TestReferenceAccessMethodBtreeSmoke$'
make check RUN='^TestReferenceAccessMethodBtreeSmoke$' REFERENCE=1
make check RUN='^TestReferenceAccessMethod(Dependencies|Controls)$'
make check RUN='^TestReferenceAccessMethod(Dependencies|Controls)$' REFERENCE=1
make check RUN='^TestAccessMethod(Descriptor|HandlerRegistration|IndexRouting|DropClosure|CaptureReplacement|Property|Grammar)$'
make check RUN='^TestAccessMethod(IndexRouting|DropClosure|CaptureReplacement)$' RACE=1
make check RUN='^TestReferenceAccessMethodCapabilities$'
make check RUN='^TestReferenceAccessMethodCapabilities$' REFERENCE=1
make check RUN='^TestAccessMethod(HeapOperations|ExclusionDescriptorPrecedence)$'
make check RUN='^TestAccessMethod(HeapOperations|ExclusionDescriptorPrecedence)$' RACE=1
make check RUN='^TestLiveVersionOverlayAndEnds$'
```

Structural units supplement public results with actual support351 and sorted live
row/version membership4→7, old-root immutability, exact independent defaults,
shared dependency closure, retained capture values and typed method remapping.
HeapOperations independently tests real version/frontier/end/private-overlay
liveness, concrete source selection, immutable roots and genuine handler
validation. ExclusionDescriptorPrecedence stages actual btree exclusion backing/
class/support under custom native gin/brin names, preserving unresolved/kind errors.
The owning [reference README](../../reference/indexes/README.md#access-method-definitions)
is authoritative for the complete current selector inventory and smoke expectations.
Query correctness is not a claim that the optimizer chose an index; ordinary
B-tree queries retain their existing scan/filter/sort behavior.
