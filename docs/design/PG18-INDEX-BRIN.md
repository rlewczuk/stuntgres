# BRIN min/max indexes

## Authority and admission

BRIN is an index implementation selected by the captured access-method descriptor,
not by an unresolved method spelling. The builtin method is OID 3580 with handler
335. Declared SQL method identity remains separate from the finite native method.
Keys, expressions, predicates, namespace resolution, ownership, locks and DDL
staging use the ordinary index owners.

The immutable catalog owns operator classes, defaults, family membership and
support identities. BRIN min/max captures the five comparison strategies and
four genuine summary operations: `brin_minmax_opcinfo` (3383),
`brin_minmax_add_value` (3384), `brin_minmax_consistent` (3385) and
`brin_minmax_union` (3386). Their internal signatures are summary contracts,
not scalar B-tree comparator callbacks. Native binding validates every selected
member and operation even on an empty or all-NULL build. Typed scalar datum
comparison and collation remain `sqltype` responsibilities.

Released identities are from PostgreSQL 18.6 UTF8/C catalog observations and
[REL_18_6 catalog declarations](https://github.com/postgres/postgres/tree/REL_18_6/src/include/catalog).
Real diagnostic-only classes retain their actual catalog identity without
claiming a native summary algorithm. In particular, inet's default remains
`inet_inclusion_ops`; explicit `inet_minmax_ops` selects min/max. Internal
`"char"` has a datum/comparison provider independently of stored-column admission.
All represented builtin BRIN classes have `opckeytype` equal to `opcintype`,
including diagnostic-only algorithms. This released catalog storage identity is
captured in the class descriptor; it does not imply a physical page layout.

Multicolumn min/max summarizes each key independently. UNIQUE, INCLUDE,
exclusion constraints, explicit ASC/DESC and NULLS FIRST/LAST are rejected by the
method-specific admission phases. Class and input resolution precede key-order
rejection; reloption validation precedes key binding; name collision precedes
class-parameter rejection. Valid predicates without a family strategy remain
ordinary SQL, not invalid index operators.

## Logical ranges and immutable publication

One slot in the authoritative `storage.Relation.Versions` vector is one logical
page. Range ordinal is `slot / EffectivePagesPerRange`. This is a deterministic
logical mapping, not PostgreSQL heap-page density, ctid, disk layout or a global
row-ID allocator. Dead slots do not renumber ranges. Additions and same-slot
replacements use the ordinary `PublicationSlot` manifest.

`storage.IndexRoot.Brin` holds effective width, immutable typed key bindings,
range summaries and the captured storage identity, generation, prefix length
and end root. A summarized range records NULL/non-NULL flags and per-key extrema;
an all-NULL range has no extrema. Empty builds still have a real root. No
per-row key vector or parallel row store is retained.

Build summarizes all existing ranges, including the incomplete tail, from live
qualifying rows. Publication extends summarized ranges for inserted and updated
successors. Ended predecessors and deletions can leave conservative bounds and
flags. A newly occupied range may be unsummarized, never falsely empty. Each
publication copies changed summary containers once and shares untouched immutable
payloads. Evaluation, comparison, allocation and failure occur before final
installation; the existing row/catalog/index fences and atomic install remain
the only publication authority. Transaction and savepoint roots use the same
carrier and do not mutate committed or retained roots.

`autosummarize=on` has a deterministic logical effect: when a publication reaches
new ranges it summarizes each previous occupied unsummarized range against the
final candidate slots. The newly opened tail can remain unsummarized. The flag
is read from current descriptor options; setting it alone does not rebuild.
This behavior does not introduce an autovacuum worker or queue.

## Candidate consistency and recheck

For a non-NULL query q, a summarized nonempty interval [min,max] is a candidate
for `< q` iff min < q, for `<= q` iff min <= q, for `= q` iff min <= q <= max,
for `>= q` iff max >= q, and for `> q` iff max > q. NULL predicates inspect the
conservative NULL/non-NULL flags. Unsummarized or uncovered ranges always remain
candidates. A nonmember strategy cannot prune. Conjunction can discard a range
only when a usable key condition definitely fails.

Candidates are lossy: [1,9] can match `=5` even when no row contains 5. Consumers
must recheck ordinary predicates on their selected visible rows. Existing SELECT
binding, filtering, ordering and snapshots remain result authority; no forced
planner path or performance guarantee follows from an index definition.

## Options, rebuilding and transport

`pages_per_range` is an integer in 1–131072, default 128; `autosummarize` is a
boolean, default off. Existing reloption numeric/boolean parsing and lexical
preservation are shared with the other methods. The descriptor's `StorageOptions`
is configured intent. ALTER SET/RESET changes intent atomically, not effective
width. DML continues using the captured root width until REINDEX or a genuine
layout rebuild uses current intent. Autosummarize alone never forces resizing.

Rewrites and ordinary rebuilds summarize the candidate destination vector.
VACUUM compaction reconstructs destination range ordinals from its actual
old-to-new slot manifest, deduplicating contributing source ranges and unioning
their produced conservative summaries. Any unsummarized or uncovered contributor
keeps the destination unsummarized. This preserves expression/predicate truths
without rerunning SQL during VACUUM; extrema may remain conservative. Compaction
uses configured width, whereas freeze-only work preserves effective width.

Native replacement rebuilds BRIN in the admitted owner-repair phase against the
final destination rows before acquiring the target lock. Locked remint and
same-layout domain-generation transport only reown already-produced summaries
when the full source-to-destination slot manifest proves exact ordinal
preservation. They rebind typed providers/storage identity and import extrema
through the existing datum owner, without SQL callbacks or per-row key storage.
An unproved layout transition cannot silently reuse source range IDs.

The owner-level lifecycle witnesses use real callable keys and partial predicates
with sequence-observed domain CHECKs, rather than synthetic evaluation counters.
Freeze-only work and compaction preserve those already-produced truths; canceled
vacuum work returns no installable pass and leaves captured roots/rows unchanged.
Same-ID domain alteration reowns extrema and comparison providers without another
CHECK event, even when new domain constraints reject fresh inputs. Native Restore
forces actual retained TEMP collisions and checks rebuilt compact destination
extrema, remapped domain/datum/provider identities, and later publication.
Sequence state distinguishes admitted repair from prohibited recipe replay during remint.
Successful private-write commit is checked against the installed root, separately
from savepoint and full-rollback isolation.

Dump retains the existing canonical CREATE INDEX followed by owner-correct
ALTER INDEX SET replay. Restore therefore initially builds with the CREATE's
default width before replaying configured intent; logical range partitions need
not equal the source. Configured options, SQL identities, results, later
maintenance and retained source rows survive the existing atomic native-control
contracts.
