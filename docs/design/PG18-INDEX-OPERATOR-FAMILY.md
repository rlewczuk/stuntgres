# PG18 operator-family catalog and registered support

## Boundary

This feature owns CREATE/ALTER/DROP OPERATOR FAMILY for btree, hash and the
implemented logical GiST methods. The operator-class boundary owns
custom class DDL, FAMILY association and new custom-class indexes. A family is
not a class. No fabricated class, reassigned builtin class or public test-only
association supplies that downstream witness.

The PostgreSQL18.6 UTF8/C oracle independently recorded49 implemented families,
292 operator members and185 support members. The compiled seed contains those
exact identities and fields, including optional and cross-type rows. Generated
initdb member OIDs are observed release facts, not guessed allocator values.
The existing class metadata inventory supplies the builtin class associations.
This includes the existing MACADDR class10024, family1984 and its five operators
and three support rows. Routine836 compares the unsigned six octets in network
order through the genuine native provider; optional sortsupport3359 and
equal-image5051 remain metadata-only, not invented physical callbacks. No MACADDR
hash class is advertised.

## One immutable catalog authority

`catalog.OperatorFamily`, `OperatorFamilyOperator` and `OperatorFamilySupport`
are ordinary separately addressed descriptors. Family keys contain namespace,
method and name. Member primary keys contain family, associated pair and number;
operator purpose filters reads, not primary identity. A secondary operator key
is `(operator,purpose,family)`. `Read.OperatorFamily`, `Read.FamilyOperator` and
`Read.FamilySupport` select real descriptors from the same name/descriptor maps
used by Candidate publication; there is no second runtime resolver.

Support formals/result remain routine metadata, distinct from the member's
associated types. The existing private builtin identity inventory embeds genuine
result OIDs and reuses its existing OID index through
`catalog.BuiltinRoutineResultByOID`; there is no family-only result allowlist.
All3397 existing lookup identities were reconciled by OID/namespace/name/formals
against pinned18.6, with no drift or missing entries;16 additional oracle
identities were not imported. Numeric results include unsupported/pseudo/array
types without requiring a local descriptor. Lookup-only hashmacaddr399 returns23
and is admissible as hash slot1; int8inc1219 and int8abs1230 return20 and receive
the real wrong-result diagnostic. None gains a provider, scalar call or SQL
pg_proc row. Genuine executable declarations/providers remain owned by
routine/functions. The single catalog-aware selector lives in
`routine/index_support_binding.go`; functions remains catalog-free. Its
BindClassSupport/BindIndexOrdering/BindPartitionHash/BindIndexForeignKey/
BindExclusionOperator routes take one immutable catalog.Read. Finite native
routine identities/signatures select actual operations, not spelling or a
key-type fallback. IndexComparison, IndexHash and PartitionHash capture the
selected family/member/routine revisions and typed operation; no per-row
lookup, scalar frame or permission check occurs. Genuine strict scalar wrappers
are exactly btint4cmp351, hashint4450 and hashint4extended425. The native
int4mi181 provider supplies the package-level selected-routine contrast; its
operator declaration does not additionally expose an `int4mi(...)` scalar call.
The public scalar witness compares against ordinary subtraction instead.
GiST remains logical; hash exclusion does not invent a same-named btree class.
Existing cross-type foreign-key comparisons use real registered family members.

Seed member payloads and precomputed name keys are immutable process-wide data;
family principal references belong to each engine. Startup uses compiled data
and standard-library production dependencies, with no source parsing, oracle
connection or per-engine catalog derivation. The root's startup benchmarks
remain the authority for the target below1ms; this document is not timing proof.

## Admission and atomic publication

Positioned parser items retain omitted/explicit association, routine/operator
signatures, purpose and sort family. Phase1 resolves and validates the complete
SQL list in encounter order, including same-list primary-key duplicates; DROP
collects requested keys without looking up stored members or pins. Phase2 checks
operators before procedures, retaining encounter order within each kind. Stored
primary/secondary conflicts and each DROP target's missing/pin/RESTRICT checks
therefore cannot mask a later phase1 error. Secondary operator conflicts remain
23505 storage errors, not primary-list42P17 errors. Phase3 alone allocates member
identities and stages changes; ordinary CatalogStage owns rollback, cancellation,
conflict validation and one publication. No valid prefix escapes a failed list.

ADD FUNCTION resolves the routine/formals before explicit associated types.
Association processing resolves the first two types (one means an equal pair)
before rejecting more than two, without resolving a third type. ADD OPERATOR
resolves the operator and any named btree sort family before binary/result/method
capability checks. Per-target RESTRICT admission reuses the common dependency
closure through the engine adapter; final removal still uses DropTarget and the
ordinary common closure, never another deletion authority.

Method limits are btree strategies1–5/support1–6, hash strategy1/support1–3,
GiST strategies1–32767/support1–12. Search operators are binary Boolean;
ordering operators require method capability and a btree sort family.
ADD applies released `assignProcTypes` admission, not a complete class validator.
Explicit associated types can differ from formals. Incomplete families are legal;
metadata registration does not promise physical optional callback execution.

CREATE resolves its creation namespace and schema CREATE authority, then the
access method, then superuser authority. Member ALTER resolves method/family
before superuser authority. Rename, owner/schema and DROP use ordinary
object/schema/role authorities. Direct and prepared utilities authorize at
execution. CREATE/ALTER/DROP share the existing Journal/Finish path in
autocommit, SQL BEGIN, Go BeginTx and retained protocol messages. Later owner
commands see private members/providers; peers see only complete committed
groups. Savepoints and rollback restore the original identities and dependencies.
Names include exact SQL method signatures; member claims identify actual
family/pair/slot keys. Nonpinned provider AS and retirement AX waits occur on
the existing ticket outside Engine.mu, followed by original-identity checks and
grant-time retirement closure capture, never name-based retry or adoption.

Family DROP resolves the access method strictly before looking up the family.
`IF EXISTS` skips an absent explicitly named schema with a schema-specific
NOTICE, or an absent family in an existing schema with the family-specific
NOTICE. It never suppresses schema USAGE denial or an unknown access method.
ALTER and strict DROP namespace/permission errors retain their SQLSTATE and
message without an expression position. Shared family identity lookup passes an
unpositioned namespace name to the existing authority without mutating the AST;
other namespace callers retain their diagnostic context. Direct and
prepared `ReviewMissingSchema` proof checks all diagnostic fields, unchanged
family/member catalog rows and identities, and subsequent statement execution.

Support-function identity lookup also uses the positionless utility context.
An absent operator namespace instead means no matching typed operator (`42883`);
family and function lookup retain `3F000`. These distinct released outcomes use
the same namespace authority; authorization and other failures remain errors.

All three family utility forms prepare without catalog mutation, reject execution
in read-only transactions first, and retain native `0A000` rejection in explicit
read-write blocks. PostgreSQL permits the latter; this is an explicit native
capability boundary, not a reason to widen transaction admission.

Family descriptors retain validated engine-local owners and namespaces. Operator
and support member descriptors deliberately have neither owners nor ACLs: common
ACL validation treats them as ownerless, like text-search templates, while their
catalog providers still validate the owning family and member references.
Member authorization remains the family utility's superuser check, not synthetic
member permission state.

## Dependencies and pins

Loose member→family and member→implementation edges are AUTO. The insertion-time
`InternalOwner` is zero or an exact class/family ObjectAddress. Same-type required
btree/hash members use the creating class or the method's physical403/405 ALTER
class lookup, not a same-algorithm SQL alias. Optional and cross-type members stay
loose. GiST operators stay loose; required support1,2,5,6,7 owns an INTERNAL class
edge for CREATE CLASS and family edge for ALTER FAMILY. Optional GiST support
stays loose. Existing members are never retroactively hardened. Hard members
have NORMAL implementation edges. Explicit associated-type and sort-family edges
are AUTO for loose members and NORMAL for hard members. Direct type edges are
omitted when pinned or already covered by implementation formals/operator
inputs; equal left/right association emits at most one direct edge.

Original initdb families and members are pinned, including optional and
cross-type rows. Pinning is not evidence of a traversed class dependency.
The original family-only witnesses use pinned rows, new loose optional/cross-type
members and custom-operator AUTO removal. Class and transactional definition
coverage additionally checks class-owned and family-owned hard members, grouped
DROP refusal/recovery and whole-family retirement. Dropping a family removes
its automatic/internal closure and preserves underlying operators/routines.
Family, operator-member and support-member roots all enter the common drop
provider's descriptor-removal phase after dependency checks. Recording a drop
target alone is not a mutation; removal must reach Candidate publication and
retire the ordinary descriptor/name/dependency indexes atomically.

## Invalidation and retained state

Metadata sources capture the current ordinary catalog. Held metadata statements
therefore see member absence/reADD identity and current names, while returned
Rows retain their captured values. Candidate failure preserves original names,
descriptors and revisions. Loose unrelated family changes do not mutate an
existing builtin ordered/hashed root or its provider capture.

There is deliberately no blanket warmed-opclass support-cache flush: released
`LookupOpclassInfo` explicitly has no cache invalidation mechanism. Fresh and
destination reconstruction select consistent registered support through the
existing publication owners. No changed provider is paired with incompatible
old root contents.

Trusted replacement validation still supplies the destination catalog through
its access view so unique/exclusion roots bind destination support. A nil
disclosure environment marks trusted publication; it does not remove the
catalog needed for provider binding.

## Replay and ownership

Bulk ownership uses the ordinary ownership-root classifier, descriptor ACL
projection and role staging. The existing principal-remap descriptor switch copies
family owners using the seeded-owner mapper; operator/support members are explicitly
ownerless and need no principal rewrite. Family/member identity remapping follows
the ordinary replacement map, including namespace/type/operator/routine/sort-family
refs and typed InternalOwner addresses; source descriptors are never edited.

Dump collection validates family descriptors and omits unchanged original seeds.
Admitted builtin name/schema/owner deltas retain pinned identities: all moving
originals first receive collision-free method/namespace-keyed temporary names
in the existing seed-staging section, then ordinary graph events restore final
schema/name/owner before loose member events. Current/custom/canonical/reserved
names prevent collisions and permit swaps without another scheduler. Custom
families and members also enter the existing declaration graph. Family events precede
member events; class-owned members fold into CREATE CLASS, while family-owned
and loose members emit ALTER FAMILY events. Actual custom type/operator/routine/
sort-family dependencies precede their consumers. SQL uses current qualified
names, explicit association and true routine formals, under bootstrap construction
with normal owner finalization. Additions to builtin families emit ALTER events.
The native roundtrip test also executes the actual emitted bytes on pinned PG.
Dump does not create cluster-global roles: destination owners must already exist,
as for other owned catalog objects. Seed replay emits an owner transfer only when
ownership differs from bootstrap, not merely because a seed was renamed or moved.

## Proof ownership

Public acceptance is in untagged `reference/indexes/operator_family*_test.go`:
Definitions, Methods, Diagnostics, Metadata, BuiltinMetadata, Dependencies,
PinnedDependencies, Atomicity, Authority, MetadataInvalidation, Notices,
TransactionBoundaries, Roundtrip, SeedRoundtrip, Smoke and SupportRoutines. These carry
independent rows/types/diagnostics before backend comparison.

Supplementary package units are TestOperatorFamilyCatalog,
TestOperatorFamilyValidation, TestOperatorFamilyMemberPhases,
TestOperatorFamilyAssociationResolution, TestBuiltinRoutineResultInventory
and TestOperatorFamilyPublication. Provider-owned
inventory/execution/root units prove actual comparator/hash identity and
noninteger consumer preservation. Smoke expects integer strategy operators
97/523/96/525/521, routine351, SAME held-query DROP/reADD identity refresh,
atomic mixed-list failure and continued builtin btree/hash maintenance.

Root owns parser generation, formatting and all verification. Selected native
and REFERENCE=1 groups, selected publication RACE=1 proof, whole-project
Docker-free quickcheck and post-integration finalcheck are distinct gates.
See `internal/engine/index/README.md` for exact selected commands. No passing
verification or benchmark claim is made by this design document.

## Release references

- [CREATE](https://www.postgresql.org/docs/18/sql-createopfamily.html),
  [ALTER](https://www.postgresql.org/docs/18/sql-alteropfamily.html),
  [DROP](https://www.postgresql.org/docs/18/sql-dropopfamily.html) OPERATOR FAMILY.
- [Index support functions](https://www.postgresql.org/docs/18/xindex.html).
- [pg_opfamily](https://www.postgresql.org/docs/18/catalog-pg-opfamily.html),
  [pg_amop](https://www.postgresql.org/docs/18/catalog-pg-amop.html),
  [pg_amproc](https://www.postgresql.org/docs/18/catalog-pg-amproc.html).
- REL_18_6 `opclasscmds.c` assignOperTypes/assignProcTypes/storeOperators/
  storeProcedures; `nbtvalidate.c` btadjustmembers; `hashvalidate.c`
  hashadjustmembers; `catalog.c` IsPinnedObject; `relcache.c` LookupOpclassInfo.
