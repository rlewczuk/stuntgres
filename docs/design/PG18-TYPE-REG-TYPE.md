# PostgreSQL 18 REGTYPE identity

## Authority and inventory

The selected implementation reuses immutable sqltype descriptors, catalog type
wrappers, staged publication, effective type lookup and ordinary expressions.
`regtype-pg18-inventory.tsv` records the pinned 621 identity facts (198 fixed,
423 initialized/allocated); it is evidence, not runtime input. The independent
reference initialized expectations do not import the production facts.

`builtin_identities.go` initializes those catalog identities while retaining
existing executable descriptor instances. Lookup-only identities are not concrete
values: domains carry their actual base identity, true arrays carry reciprocal
element/array links, and scalar typelem facts do not manufacture array authority.
Only released supported storage families acquire executable array codecs.
Information_schema has its initialized namespace and public USAGE. Missing
catalog relations are not synthesized as executable query sources.

Real user table/view descriptors own separate TypeClassID row and array identities.
Their stage precedes stored table definitions for legal self references. Rename
remints immutable name descriptors while preserving OIDs; owner/schema movement
and removal update both links. Existing catalog projections link their actual
seeded row identity. Ordinary sequences and indexes do not acquire row types.
Enum creation also publishes its real lookup-only associated array, without
admitting enum array storage.

`displaceAssociatedArrayLocked` runs within the existing CatalogStage before
table/query-table/view/domain/enum creation-name reservation. Only a reciprocal
element-associated array can move; independently named conflicts still fail.
The incumbent array name seeds the existing free-name allocator. Existing
`Array`/`LookupArray` constructors retain OID, element, capabilities and delimiter
while refreshing the displaced name and database metadata label; catalog ownership
and provider links remain unchanged. Staged lookup includes reserved names and vacancies while publication
retains the original pointer/touched-object conflict fences. Existing type/scalar
and expression reownership seams retarget the impacted stored generation closure;
retained descriptors and cursor snapshots remain unchanged. Failed declarations
publish neither the displacement nor partial new identities.
Fresh executable array columns use the displaced database metadata label; retained
cursor generations keep their original label and values. Published view source
columns retain the existing native nullable metadata even for constant definitions;
newly computed expressions retain their independent metadata contract.

## Input, unsigned value and output

The existing lexer/goyacc parser has a private standalone type entry, isolated
from ordinary SQL annotation normalization. It consumes complete decoded input
and preserves alias provenance, parts, modifiers, array bounds and SETOF syntax.
The root regenerates the existing grammar output; there is no parallel parser or
generator. Exact dash/digit-only input uses the existing unsigned OID codec;
other input resolves the standalone tree under captured namespace authority and
session database. SQL aliases force pg_catalog. Qualified lookup checks schema
USAGE, current-database prefixes are supported and foreign references fail before
schema lookup. Provider modifiers validate and are discarded, preserving normal
provider diagnostics and notices.

REGTYPE values carry an inline unsigned OID and validity marker. Decimal text is
created at codec/output boundaries, not retained as a display datum. Direct casts
follow released context edges; equality/grouping/ordering use unsigned identity.
Recursive array/domain import and output use the same captured authority seams as
existing catalog identifiers. Native exports and ScanType are strings. Array of
user domain and domain over REGTYPE[] retain their different metadata identities.

Output captures the catalog and effective visibility path before rows escape.
Builtin format_type aliases are canonical; actual user names are visibility-aware,
qualified/quoted when necessary, and arrays use element[] display. Unknown IDs
remain decimal, zero dash. Retained rows never consult a later live catalog.

`CoercionPlan.RequiresCatalogOutput` follows the selected IO operation and its
recursive catalog-identifier source, not a TEXT-only destination check. Explicit
VARCHAR and admitted string assignment (including domain-wrapped REGTYPE) request
the same statement-owned snapshot. Binding, stored-expression rendering and the
manual ALTER-column assignment plan share this predicate; identity and numeric/OID
conversions do not request symbolic IO.
Manual UPDATE and query-output assignment plans propagate that same selected
demand. Stored generated destination assignments consume their existing
`catalogSnapshot` manifest alongside defaults and CHECKs. PostgreSQL admits
REGTYPE/domain-to-TEXT/VARCHAR generated destination assignment, but rejects
explicit IO inside the generation expression with42P17
`generation expression is not immutable`; declaration failure publishes nothing.

## Held analysis and notices

A private regtype analysis cell owns the entire source-offset literal group.
Its key is the executing principal, database and ordered effective namespace
identities/implicit path semantics. Actual qualified input lookup records its
namespace addresses, including recursive array leaves, through the existing
captured resolver. Reuse checks those namespaces' existence and current USAGE,
not global role revision, general catalog generation or referent presence.
Unchanged authority reuses OIDs after drop/recreation and unrelated role DDL,
without repeating analysis notices. Qualified schema ownership loss invalidates
the group and reanalysis reports the outer literal's 42501 Position; frame input
retains its portal Context. Owner-role recovery admits lookup again.
Changed authority binds a complete compatible group and publishes only on
successful analysis. Public prepared signatures remain immutable and existing
Engine.mu/prepared guards govern publication.

Literal modifier notices occur during analysis; unchanged held execution reuses
the group without notices. Per-slot frame authority adds the ordinary unnamed
portal notice Context. Runtime TEXT conversion uses the unchanged environment,
so it does not acquire portal Context. Parser Context and decoded positions remain
available, while literal errors transpose Position to the outer source literal.

## Stored references and persistence

The existing scalar renderer records known REGTYPE leaves as TypeClassID
references, separately from declared datatype authority. Recursive domain/array
leaves and stored numeric casts participate; zero/unknown IDs do not. Stored data
alone adds no dependency. Existing type/expression closure follows those edges and
an owning relation's row identity for RESTRICT/CASCADE.

Structural Dump input uses actual fully qualified scalar catalog names and
recursively qualified `element[]` for reciprocal associated-array REGTYPE
constants, never source OIDs or search-path-dependent symbolic display aliases.
Generated OIDs and internal array names may be reminted during logical replay;
exact logical identity, reciprocal relationships, stored values and metadata
remain preserved. Historical generated aliases are not persistence guarantees.
Explicit named-array domain bases retain dimensions zero, while explicit bounds
retain their declared count. A transient prediction seeded from `sqltype.Builtins`
simulates domain/enum creators in their existing topological order using the
same associated-name chooser as CatalogStage. Requested-name displacement happens
before base rendering; the predicted alias is keyed by captured TypeClassID.
Array allocation follows scalar reservation. This is rendering state only, not
a second catalog, OID authority, private DDL or naming-history recipe.
Referenced row types impose owning-relation predecessors. CHECK/generated/view edges remain
mandatory; legal default-only cycles defer identifier defaults until referents
exist, then replay ordinary ALTER COLUMN SET DEFAULT.

Restore binds real SQL in its isolated catalog. Reownership updates row/array
links, namespaces, logical datatype pointers and recursive REGTYPE leaves using
TypeClassID mappings. RelationClassID mappings with the same numeric ID are not
fallbacks. Immutable source payloads/eager snapshots are retained. Existing atomic
catalog replacement governs invalid statements, trailing input and cancellation.

For a failing CREATE VIEW with a missing source column, native's existing eager
view binding reports 42703 during Prepare, whereas PostgreSQL's utility reports
it during Execute. The independent diagnostic fields and unchanged associated
array identity are required in both routes. This specific preexisting stage
difference does not exempt other DDL failures or expand view timing behavior.

## Verification contract

This document describes implementation, not passing evidence. Root-owned checks
must regenerate/format first, then validate full reference definitions in both
native and pinned PostgreSQL modes (900-second deadline per quickcheck):

```sh
make generate
make quickcheck PACKAGES='./internal/sqltype ./internal/parser ./internal/engine' RUN='^TestRegtype' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtype' VERBOSE=0
make quickcheck PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
make quickcheck REFERENCE=1 PACKAGES=./reference RUN='^TestReferenceRegtypeFeatureSmoke$' VERBOSE=0
```

Expected assertions cover complete identity lookup, standalone diagnostics,
unsigned codecs, direct/prepared metadata, held path/drop/recreation/notice
lifecycle, recursive authority, stored dependency diagnostics, actual native
Dump/PostgreSQL replay, TypeClassID remapping and failure atomicity. Shared-seam
regressions and ordinary quickcheck precede review; final postmerge fullcheck has
one aggregate 1800-second deadline. No acceptance is reduced for native failures.
