# Operator-class definitions

## Contract and authority

`CREATE OPERATOR CLASS`, `ALTER OPERATOR CLASS` (rename, owner and schema), and
`DROP OPERATOR CLASS` extend the existing operator-family implementation. The
syntax and admission contract follow PostgreSQL 18 [CREATE](https://www.postgresql.org/docs/18/sql-createopclass.html),
[ALTER](https://www.postgresql.org/docs/18/sql-alteropclass.html), and
[DROP](https://www.postgresql.org/docs/18/sql-dropopclass.html). The exact catalog
shape is [pg_opclass](https://www.postgresql.org/docs/18/catalog-pg-opclass.html).

`catalog.OperatorClass` is the live immutable authority: object address2616,
namespace, name, revision, owner, exact method OID, containing family OID, input
type OID, default flag, and storage type OID. Storage0 means the input type.
Class names are keyed by `(namespace, method OID, name)`. DEFAULT membership is a
derived immutable `(method OID,input type OID)` bucket of actual class OIDs,
ordered by decoded name, namespace OID and OID, not a unique secondary name.
Canonical writes, CloneState, Candidate projection, journal marks and bulk/reset/
remap maintain the same index. Rename/schema/owner replacement retains OID and
family identity. Moving a class never moves its family.

The63 compiled supported class facts seed this authority alongside the existing
49 families and released member facts. Compiled names and defaults are bootstrap
inputs, not a second live lookup. Dropping or moving a descriptor cannot reveal a
static fallback. `pg_opclass`, object-address lookup, identity presentation,
explicit resolution and default selection all read current descriptors. Original
bootstrap classes remain pinned; their supported rename/schema/owner deltas are
serialized rather than recreated under fabricated identities.

## Exact access method versus physical implementation

`catalog.AccessMethod` consolidates the compiled OID/name/handler/kind and
physical implementation facts. `IndexDefinition.MethodOID` retains exact
identity; `Method` dispatches the existing btree/hash/bounded GiST algorithms.
MethodOID is never reconstructed from the physical enum in a stored definition.
Class/default lookup, family coherence and captured support use exact identity.
Registered CREATE/DROP ACCESS METHOD definitions supply exact handler-backed SQL
identities; there is no arbitrary public native-handler registration API.

`routine.TestOperatorClassMethodIdentity` supplies private immutable method facts
with OIDs403 and90000 sharing btree mechanics. `Read.IndexClassForMethod` is the
value-fact constructor also used by ordinary compiled-fact `Read.IndexClass`.
The production binder's private fact-taking path constructs support captures
through `selectNativeIndexSupport`, using genuine Procedure351/181 descriptors.
Same-spelling classes/defaults and families/members remain independent, including
family/member/routine revisions; compare(1,3) is-1 versus-2. Crossed method facts,
family/input captures, absent members/procedures and invalid native descriptors
fail without fallback. No fixture installs support captures manually.
`TestOperatorClassCatalogMethodIdentity` separately checks catalog name/default
keys, construction and retained method identity, not executable selection.

## Admission and atomic publication

The parser retains class names, input type, DEFAULT, USING, optional FAMILY,
and ordered positioned OPERATOR/FUNCTION/STORAGE items. Utility preparation is
quiet. CREATE/ALTER/DROP use the existing transaction Journal and Finish path in
autocommit, SQL/Go transactions and retained messages; Execute checks current
actor/schema/provider authority. Private reads include earlier owner changes,
and savepoints or rollback restore the complete graph without replacing identities.

CREATE resolves the creation namespace and CREATE privilege before method and
superuser authority, then input type and family. Without FAMILY, the same-named
family in the class creation namespace is reused, or a private implicit family
is staged. A search-path family in another namespace is not substituted.
Existing member resolvers retain the distinction between function formal
arguments and associated left/right types; class input supplies the released
fallback where required. Entire-list admission precedes operator and procedure
storage phases. Class-name/default collisions, storage normalization and member
slot collisions never publish a valid prefix.

The descriptor, any implicit family, member rows, names, derived default bucket,
dependencies and resulting index roots publish through one ordinary candidate.
Validation failure or canceled publication leaves the incumbent catalog, data,
keys and roots unchanged. Incomplete definitions are legal.
CREATE does not prove completeness or mathematical consistency; index binding
requires the real executable members it actually needs. An admitted incomplete
concrete enum class is not a claim that a concrete enum index can be built.

Sequential CREATE checks `AnyDefaultOperatorClass` in the current private view
and rejects an existing default. Two independently admitted distinct DEFAULT
classes can both commit; there is no absent-default name fence or default-only
lock. Required `DefaultIndexClass`/`DefaultIndexClassID` select an exact domain-base
input first: multiple exact defaults report42710. Only applicable shipped
compatible fallbacks participate after exact lookup; nonunique compatible
candidates yield no selection and the caller's ordinary42704 missing-default
diagnostic. Unrelated fallback buckets have no effect. Explicit named classes
bypass default lookup. `DefaultOperatorClass` and
`UniqueDefaultIndexClass` are unique metadata predicates for optional proof and
deparse, not choosers that hide ambiguity. Stored indexes retain their selected
class/provider rather than reselecting a new default.

## Executable support and consumers

Catalog-backed default selection preserves domain flattening, concrete enum to
anyenum adaptation, binary-compatible input aliases and bounded GiST range
adaptation. An explicit class is selected by current namespace and exact method.
`sqltype.IndexClass` is a value capture with OID/method/family/input/storage and
provider flags, not a mutable catalog resolver. Current names are presentation
only. Inference and compatibility compare exact family/method identities.

`routine.BindClassSupport` checks the current class/family relationship and
selects the registered family member and real native procedure identity,
signature, result and revisions. There is no cloning of builtin int4 semantics
by class spelling, default fallback, SQL routine frame, per-row catalog lookup
or per-invocation EXECUTE check. Native support selection does not expose a new
scalar SQL function. Arbitrary user SQL callback execution is not added.

The same path covers ordinary and expression indexes, maintained roots, UNIQUE
and ON CONFLICT, hash, exclusion, FK comparison, partitioning and its proofs,
ALTER TYPE reconstruction, LIKE, REINDEX and native replacement. Grouping/hash
compatibility for uniqueness claims is captured in a32-bit key mask at binding,
not recomputed by name/default lookup for every row. Nondefault equality can use
the existing comparer bucket; network datums retain their no-generic-grouping
boundary. Logical btree SELECT correctness still uses ordinary snapshot
scan/filter/sort; this work does not introduce a query optimizer or physical
PostgreSQL index pages.

`TestOperatorClassSupportExecution` constructs genuine custom descriptors,
selects351 versus181, builds roots over existing rows, and maintains insertion
while preserving the old root. Independent row identities are2,1,4,3 before
insertion and5,2,1,4,3 after it. Missing support and crossed family captures cannot
bind a builtin fallback. The public subtraction comparator overflow witness is
oracle-grounded: comparing opposite INT4 extremes can produce22003 on build or
maintained INSERT, atomically preserving prior rows/root.

## Common dependency graph and ownership

Class2616 has NORMAL namespace/input/storage dependencies and AUTO containing
family dependency. The method relationship is carried through that family.
Actual indexes have NORMAL references to their class; partition keys also retain
class dependencies. Required class-owned member rows have INTERNAL class and
NORMAL implementation dependencies. The existing method adjustment retains
optional and cross-type loose members instead of retroactively hardening them.
GiST support1,2,5,6,7 has INTERNAL class ownership when inserted by CREATE CLASS,
or INTERNAL family ownership when inserted by ALTER FAMILY. GiST operators and
optional support stay loose. `InternalOwner` records the exact address at
insertion; ALTER's btree/hash lookup uses physical403/405 class identity, not a
later SQL-alias class. Validation and remapping preserve the actual owner kind.

DROP RESTRICT of an indexed class reports the independently asserted dependent
index diagnostic. CASCADE removes the actual dependent index and required owned
members, not the containing family or underlying operators/routines. Implicit
families survive class removal too. Loose member classification is insertion-time.
Schema/principal lifecycle, REASSIGN/DROP OWNED, ACL decisions, object identity,
pinning and graph retirement share ordinary subsystem machinery rather than a
parallel class deletion map. `TestOperatorClassCatalogAtomicity` exercises
canceled ordinary and duplicate-default publication plus class RESTRICT/CASCADE
through the real stage and graph with a retained custom index root.

The common removal provider accepts class descriptors directly, rejects pinned
bootstrap identities independently of their current names, and then records
ordinary candidate retirement. Class graph discovery alone is insufficient:
the same provider must accept the2616 target when executing the closure.

## Dump, Reset, Restore and destination binding

The ordinary dependency scheduler emits types/routines/operators and families
before classes, classes before dependent indexes and partitioned tables. Class
CREATE emits its class-owned entries once; loose optional/cross-type and
family-owned support rows remain separate ALTER FAMILY ADD events, preserving
the actual family INTERNAL edge. A legal class with no owned entries emits a
normalized STORAGE=input item. Current class/family names, namespaces and owners
are used, including bootstrap rename/schema/owner deltas staged collision-safely
before graph replay.

Class inputs and family signatures are catalog type identities, not necessarily
valid concrete column usages. Nonconcrete types such as `anyenum` and `internal`
are emitted with their live qualified catalog names; concrete types retain the
ordinary declaration presenter. This keeps polymorphic class/member inputs
separate from the concrete enum column whose destination identity is remapped.

Foreign replacement remaps class/family/type/member/InternalOwner/principal and
index references. Runtime class captures are reconstructed without source
support; destination reads rebuild executable support and partition comparators.
Source roots/providers are never transplanted. Failed late replay remains one
atomic replacement failure. Held metadata reads use the existing dependency and
effective-namespace machinery; returned Rows and retained roots remain immutable.
No global DDL epoch or blanket warmed-root invalidation is introduced.

Replay remains ordinary sequential SQL, without a trusted duplicate-default
admission exception or new format. A graph with multiple persistent DEFAULT
classes is not representable by sequential CREATE DEFAULT statements. Dump's
existing preflight rejects it before writer output with an ordinary native error;
TEMP namespaces and private overlays do not spuriously reject the persistent
graph. Manually supplied sequential duplicate definitions still fail42710 during
atomic Restore, leaving incumbent state and eligible private work unchanged.
After dropping a duplicate, the unique graph roundtrips actual emitted bytes.

The roundtrip reference definition replays actual native-emitted SQL on pinned
PostgreSQL, not a separately reconstructed expected script. Its enum class is
`FOR TYPE anyenum`, all five real anyenum strategy operators plus
`enum_cmp(anyenum,anyenum)`; input/member types remain3500. A real concrete user
enum table/index column supplies the separate destination type-remap witness,
including label order and maintained post-Restore insertion. A concrete-input
incomplete enum class alone cannot substitute for this executable contract.

## Proof and capability boundaries

Public shared-body definitions live in `reference/indexes/operator_class*`:
FeatureSmoke, Definitions, Diagnostics, ComparisonConsumption, Lifecycle,
Roundtrip and SeedRoundtrip. Their pinned PostgreSQL18.6 oracle discovery and
native syntax RED preceded production. Final native/reference, package unit,
race and whole-project evidence are root-owned; this design does not claim an
unexecuted verification result.
Transactional routes, concurrent default ambiguity and complete retained-index
class dependency bags are covered in `reference/object_address/transactional_*`;
`TestOperatorClassDefaultMultiplicity` and `TestOperatorFamilyInternalOwners`
exercise immutable buckets, exact ownership, replay and retirement.

Feature smoke selectors and exact commands are in the owning package READMEs.
The feature adds no new physical access method, scalar function surface,
array/record provider, arbitrary callback ABI, direct system-catalog writes or
pg_index vector projection. Existing supported btree/hash/bounded GiST provider
limits remain in force.

The existing bounded unsupported-input validation remains explicit: after
collation validation, hash array inputs and btree NAME[] inputs (including
domains) report native0A000 before class lookup. Other btree arrays do not gain
this capability branch. This uses type/provider facts, never an `array_ops`
name fallback or a fabricated live class, and preserves the existing
`TestReferenceHashIndexDiagnostics` and
`TestReferenceIndexClausesUnownedBoundaries` contracts.
