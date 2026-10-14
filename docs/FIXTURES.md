# Native fixture controls

[README](../README.md) · [Native compatibility limits](DIALECT.md)

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. They are **host-authorized**, independently of a borrowed connection's effective principal. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity, cluster roles, nonreuse counters, live identities/settings and existing pools. Dump captures before external I/O, emits persistent tables/views in creation-address order among objects whose sources are ready, and writes quoted owner-switch directives plus PostgreSQL-compatible CREATE/INSERT SQL, declaration-ordered columns and canonical literals. Explicit relation/column ACLs cannot be serialized and cause a failure before output, rather than silent loss. Dump excludes role creation, passwords, membership and settings; it is not complete `pg_dump` output, COPY, or catalog export.

Dump reconstructs renamed, absent and recreated bootstrap namespaces using ordinary
DROP/ALTER SCHEMA SQL before user schemas and tables. Retained builtin ACLs must
equal their canonical bootstrap rights with both grantor and owner-grantee
transformed to the current owner; arbitrary explicit ACLs remain unsupported and
are rejected before output. Recreated public is an ordinary namespace with absent
ACL, never silently granted PUBLIC rights. Unchanged bootstrap streams retain
their exact legacy bytes.

Ordinary partition detachment exports final standalone relation/constraint/index
state, including detached virtual subtrees, not a log of the original command.
Ready/live virtual indexes may legitimately remain invalid despite complete or
empty coverage; invalid physical indexes remain excluded. Replay classifies each
virtual tree bottom-up: incomplete/invalid-child coverage uses explicit index
attachments, complete-invalid coverage uses a real valid child's table
detach/reattach, and childless invalid coverage uses a collision-safe empty
DEFAULT that is detached and dropped within the stream. No helper object survives
successful Restore or PostgreSQL replay. Equivalent older unattached local
UNIQUE/PRIMARY providers follow reattachment. Physical backings use ADD USING
INDEX; virtual local definitions use conventional UNIQUE/PRIMARY and the existing
recursive constraint/index producer. Their generated subordinate names are
reserved and renamed to captured private identities before terminal final names.
Consumer identities remain bound to the distinct owned local tree.
Key declarations and their owning table constraints use the same captured
private-name projection, with terminal renames restoring the original names;
deferred local keys do not render an original backing against a renamed owner.
Initial table membership must not wait for a deferred local key's terminal rename:
the preserving reattachment precedes that local declaration. Original-name
REGCLASS row consumers still wait for the completed local declaration and rename.
Late dependent declarations bind against the captured private spelling before its
terminal rename; otherwise a default or view could reference a spelling already
removed. Stored original-name data follows the rename, without aliases or rebinding.
Deferred virtual-tree terminal names complete as a component-wise schema/name-ordered
group. Original-name consumers of any member wait for the whole group's tail,
so reminted root/leaf OIDs cannot move subordinate renames across data/defaults.
Decoded schema names compare first, then object names; quoted dots never merge components.
The private allowlist admits qualified unconditional ordinary DETACH, exact
captured empty DEFAULT teardown, leased ADD USING INDEX on the exact captured
physical member/index, and exact conventional virtual definition/table/subtree
origins after the intended plain tree is attached. PRIMARY replay retains existing
NOT NULL facts. Public hierarchy ADD, general DROP, CONCURRENTLY/FINALIZE and
hierarchy-FK scripts remain excluded.
Failed readers, malformed replay, cancellation or final publication fences retain
destination catalogs, data, logical roots and eager snapshots.

Feature smoke: `make check RUN='^TestReferencePartitionDetachFeatureSmoke$'` and
the same selector with `REFERENCE=1`; full controls/replay:
`make check RUN='^TestReferencePartitionDetach(Controls|Replay|ReplayLocal|ReplayVirtual)$'` in both modes.


Conversion controls serialize real qualified CREATE [DEFAULT] CONVERSION
definitions, canonical encoding strings and actual registered routine names.
Pinned seed rename/schema/owner deltas precede user definitions. Legal duplicate
defaults are replayed through collision-free helper schemas that are removed
within the stream; no provider tables, library paths or arbitrary function
definitions are serialized. Canonical unchanged seeds preserve legacy dump
bytes. Temporary definitions are omitted; live target TEMP and user OID
collisions use the existing replacement remapping frontier. Failed readers,
late bad conversion statements, cancellation and captured-principal changes
leave the destination catalog/data and returned metadata intact.
Conversion replay is part of the same bounded Restore allowlist, not a generic
script interpreter. Smoke: `make check RUN='^TestReferenceConversionFeatureSmoke$'`
and the same command with `REFERENCE=1`.


Enum declarations are emitted before dependent domains and relations, with exact
declaration-order labels, qualified current schema names and owner directives.
Dependent defaults/CHECKs/views render their captured logical type authority,
including schema rename. Cross-driver Restore remaps enum owners, descriptors
and inline ordinal values together, including dependent domains and collisions
with retained TEMP sequences. Equal label lists never authorize ordinary
cross-enum casts or prepared-name retargeting. Changed user-type ACLs are refused.
Read/parse/label/owner/cancellation failures preserve destination catalog/data
and held eager snapshots. Feature controls:
`make check RUN='^TestReferenceEnum(Controls|RestoreAtomicity|OwnerAndTemporaryRemapping)$'`
and the same command with `REFERENCE=1`.

Persistent view definitions are dumped after their relation dependencies, with
fixed output names, current bound relation identities, frozen column namespace labels, security options and
owner directives. Views are not evaluated by Dump/Restore and their output is
never serialized as table rows. Restore binds canonical CREATE VIEW in its
isolated catalog, remaps every stored source/expression/dependency identity into
the destination generation, and atomically installs definitions with ordinary
data. Derived catalog sources initialize normally and are not dumped. Restored
views remain dynamic; later base writes and dependency checks still apply.
Failed/canceled imports retain old catalogs, dependencies and held result rows.
Repeated replacement, trailing outputs, view/column rename, owner/schema changes
and security options serialize their final canonical CREATE VIEW state, not
ALTER history. Supported COLLATE identities survive reconstruction and replay.
Whole Restore may allocate new OIDs, but preserves linked identity relationships
and dynamic reads. Dump refuses explicit relation/column ACLs rather than
discarding privileges. Cyclic view definitions are legal catalog state, but
Dump returns `stuntgres: cyclic view dump dependencies`; repair the definition
before dumping. These refusals do not narrow ordinary replacement retention.
Feature roundtrip smoke: `make check RUN='^TestReferenceViewReplaceSmokeCycleReplayControls$'`.
Dump orders scalar user types, named composites, domains, tables and views through
one captured persistent provider topology. Composite members depend on their
child types/owning row relations; tables depend on column types and CHECK providers.
Views depend on table/view sources and expression providers. An identity-sequence dependency waits for the owning table
whose inline declaration creates it. A table's own identity CHECK is not a
self-edge; ordinary standalone sequences remain in the earlier creation prefix.
Creation addresses break ties between ready objects. Retained TEMP collision
remapping may move a source OID above its dependent; topology then wins so replay
remains executable, without promising allocator-relative diagnostic-order parity.
Captured scalar REGCLASS defaults and fixed early-bound `nextval` defaults may
close a dependency cycle through their provider. Dump defers only these already
admitted definitions: it omits them from CREATE TABLE and emits ALTER DEFAULT
after their providers exist. Restore uses the ordinary default binder and the
same replay eligibility decision. Required CHECK/generated dependencies are
never relaxed; cycles that remain fail before SQL is emitted.
Roundtrip and actual pinned replay:
`make check RUN='^TestReferencePGDependReviewCapturedReplay$'`;
the pinned counterpart adds `REFERENCE=1`.

Named composites replay as canonical CREATE TYPE AS declarations, never as
storage tables. Supported table/view row types are created by their real owner.
Native replacement remints/reowns reciprocal descriptors, stable member usages,
nested stored/default values and live REGCLASS/REGTYPE children together, while
held snapshots remain immutable. Plain domains over rows/composites and mixed
source-row→named-wrapper→domain→holder/view dependencies replay as ordinary SQL
on PostgreSQL. No new harness, physical ABI, constrained-domain feature or
composite-array storage is implied. Composite controls:
`make check RUN='^TestReferenceComposite(MixedControls|ControlLifetime)$'`
and the same command with `REFERENCE=1` (900 seconds each).
Composite expressions additionally retain occurrence-owned constructor shapes,
stable field/expansion attributes, genuine dot-builtin markers and exact
whole-row namespace/source/frame handles. Native definitions emit captured
expansion members explicitly, never a future widening wildcard; direct whole-row
sources emit alias.* so a same-named scalar column cannot capture the value.
Restore reowns capture usages and runtime/retained-analysis trees together.
Controls exercise two replacement cycles, future writes, held eager output,
failed/canceled publication and actual emitted SQL replay on pinned PostgreSQL.
Selected checks (900 seconds each):
`make check RUN='^TestReferenceCompositeExpr(StoredConsumers|ControlsAndLifetime|ControlRejection)$'`
and the same selected command with `REFERENCE=1`.
The two-retained-sequence, second-Dump/Restore regression and independent
PostgreSQL nested-view consumers run through the same mode-aware definition:

```sh
make check RUN='^TestReferenceViewNativeNestedSecondDumpAfterTemporaryRemap$'
make check REFERENCE=1 RUN='^TestReferenceViewNativeNestedSecondDumpAfterTemporaryRemap$'
```

Joined imports remap every source occurrence, both join children, predicates,
merged-value coercions and all frame schemas into the destination generation.
Frozen per-leaf attribute layouts and analyzed ON literals remain attached to
their logical occurrences; retained TEMP type generations do not change bags,
merged output types or canonical definition reconstruction.
Namespace labels/slots, explicit USING keys and attribute IDs are immutable
metadata without generation-specific type pointers. Full positional aliases
follow the current physical/visible order after compaction; NATURAL replays its
frozen USING keys, and merged conversions use the ordinary implicit binder.
Stored ORDER preserves qualified frozen physical/merged input-slot identity,
separately frozen output ordinals, and each key's direction and NULL policy.
Render-private USING aliases preserve synthetic merged usage when needed.

Stored view literals are serialized from creation-time analyzed values and type
usages, including temporal precision, typed NULLs and sentinel values. Relative
spellings such as `DATE 'today'` do not acquire a new value during replay.
Native remapping replaces concrete catalog type identities without reparsing or
coercing the captured datum; retained TEMP objects do not alias the old generation.
Contextual NAME, UUID, BYTEA and NUMERIC literals likewise retain their analyzed
values and distinct result types through definition reconstruction and native
Dump/Restore, including NUMERIC display scale. Public direct/prepared coverage,
including `numeric_scale`, runs in both modes:

```sh
make check RUN='^TestReferenceViewContextualScalarLiteralNativeRoundTrip$'
make check REFERENCE=1 RUN='^TestReferenceViewContextualScalarLiteralNativeRoundTrip$'
```

Restore is restricted logical replay, not arbitrary SQL script execution.
Parameters are recursively rejected. Only the engine's admitted declaration,
data, owner-directive and sequence-state replay operations are eligible; live
settings, transaction controls, shared-role mutation and arbitrary expression
producers remain excluded. Legacy creation/data-only scripts remain
bootstrap-owned.

Stored expression reconstruction preserves captured type authority and
structural carriers without evaluating definitions during Dump. Restore
validates admitted expressions through the ordinary binder/providers; replay
does not add new public casts, defaults or expression capabilities. Invalid
expressions and failed imports preserve old data/catalogs and held cursors.

Referenced roles must already exist in the target engine. Restore captures a private role
snapshot before input I/O, starts an initialized private catalog, executes through
the same SQL engine, remaps owners into target principal authority, then atomically
**replaces** descriptors, storage and epoch, never merges or resets roles/live
sessions. Missing or dropped/recreated owners and read/parse/execution/cancellation
errors leave original data untouched. Empty input clears user relations. No target
locks span external I/O. Private operation-only CREATE destination, INSERT and
database-local COMMENT bootstrap authority preserve independent schema/table owners
without grants or session identity changes. COMMENT's private marker covers lookup,
lock resolution and mutation, is cleared after each statement, and never permits
ROLE replay. Ordinary post-Restore SQL still enforces USAGE/CREATE and ownership.
Trusted INSERT permission elevation does not replace identity snapshots:
defaults and CHECK expressions observe the actual private session's
`current_user` and `session_user` from named owner directives, both during
ordinary replay and final owner-remap validation.

REGROLE row data uses symbolic live role names, recursively through arrays and
domains, and decimal fallback for unknown OIDs (zero is `-`). Cross-Driver replay
therefore resolves names to destination role identities, not source allocator
IDs; roles must be provisioned separately. REGROLE constants are prohibited only
as non-NULL scalar stored-expression leaves, not as row data or opaque array
payloads. Dump never creates roles or adds role dependencies.
Captured opaque REGROLE[] view literals retain their owning type annotation:
repeated Dump/Restore does not accumulate redundant array casts.
Regrole replay input failures retain the original full-script character Position;
native controls add no synthetic Context or unrelated diagnostic fields.

The shared object allocator skips live namespace, relation, view-rule, CHECK,
NOT NULL and default identities imported by Restore. Replacement does not rewind
the destination's nonreuse counter. Definition collisions with retained native
objects are reminted in the private candidate before installation.
Dump/Restore preserves CHECK casts/names/NO INHERIT, NOT NULL and explicit
defaults through canonical SQL replay, not raw definition OID directives.
Restored IDs may differ; current owner/attribute relationships and behavior persist.
Structural unknown-NULL provenance and raw temporal precision survive replay;
destination sizing remains an executable coercion rather than a deparse-time value.
Installed supported view options retain presence, lexical value and order through
logical Dump/Restore; absent security options are not emitted as false defaults.
Active indexes and inheritance/table/index-partition edges reconstruct pg_class
publication history during replay. Deleted-only persistent history need not
survive logical replay, while valid live TEMP descriptors retain identity,
options and sticky history under the existing dependency fences. Table-member
bounds retain typed key authority; native structural output intentionally differs
from PostgreSQL nodeToString. Arbitrary explicit relation ACLs remain outside the
serializer rather than being silently discarded; serializable baseline replay
does not promise to preserve unsupported grants.
The deparse reference matrix supplies independent release strings for admitted
persisted families/types; these goldens and its generic successful insertion are
not, by themselves, family behavioral completion. Dedicated consumer fixtures
create compact/pretty copies from actual returned SQL and require direct/prepared
accepted, rejected and NULL rows, complete logical error records, and exact old
row contents after failed INSERT/batch/UPDATE. Positive/negative NUMERIC typmods
distinguish unsized synthetic annotations from genuine explicit sizing failures.
Conditional consumers distinguish searched/simple CASE, absent ELSE, short-circuit
errors, COALESCE/NULLIF/GREATEST/LEAST and integer arithmetic boundaries.
Comment consumers control object/column/namespace/shared-role contents and retain
prepared handles across changes. Namespace/shared-role witnesses discover the
backend's own identities through existing pg_class relnamespace/relowner fields,
capture and restore prior comments, and drop the owned namespace before session
reset. They need no new catalog projection or cross-backend allocator parity.
Random consumers independently replay each backend's seeded same-call effects,
including strict NULL and failed statements; native/PostgreSQL sample equality
is deliberately not asserted.
current_schemas CHECK enforcement tests strict BOOL NULL, not NAME[] comparison.
Changing effective paths is separately observable through the same held public
read. Typed TIMESTAMP/TIMESTAMPTZ/TIMETZ AT/LOCAL consumers check result-sensitive
rows, current session zones, provider errors and unchanged typed row values.
Both session-resolved UTC abbreviation consumers and fixed IANA Etc/UTC consumers
are retained. Under UTC-2, PostgreSQL resolves the UTC abbreviation to +02;
the TIMESTAMP/TIMETZ predicates can therefore reject the same held input.
Etc/UTC retains the intended fixed-zone conversion. These are distinct runtime
witnesses, not adapter exceptions or substitutions of one zone for the other.
Native NULL SYSTEM_USER and PostgreSQL SCRAM identity are independent expectations.
Native capability exclusions remain explicit rather than being inferred from
successful expression reconstruction.
Native replay fixtures keep context-sensitive CHECK objects empty, retain literal
rows in separate default-only objects, then exercise symbolic definitions through
caller-held post-Restore DML. Replay uses its existing isolated context, not the
caller's identity/path. Actor-owned schema setup and CREATE under SET ROLE use
existing ownership routes rather than adding GRANT or ALTER TABLE OWNER support.
Changed-type default fixtures require a fresh own definition OID, unchanged
relation/attribute identity and absent old live lookup; eager original metadata
remains usable. Actual copied default text produces the expected BIGINT value,
while failed conversion preserves the old definition, type and complete rows.
Additional metadata witnesses retain integer boundary constants, typed NULL
arguments, raw temporal precision and quote/backslash spelling under both
string settings. Copied sized-numeric defaults retain preassignment values and
must still fail atomically at execution when assignment overflows.
Derived pg_constraint/pg_attrdef rows and stateless deparse functions are not dumped.
Index/view deparse functions likewise have no separate serialized state. Dump
uses current bound descriptors and canonical replay presentation, not pretty
strings or view result rows. Restore preserves index keys/unique flags and view
identities through existing remapping; rediscover destination OIDs rather than
assuming equality with the source allocator. Held function statements see fresh
catalogs, while previously returned definition strings remain owned.
Feature smoke: `make check RUN='^TestReferenceDeparseRelations(NativeControls|PreparedLifecycle|ReconstructionConsumers)$'`;
oracle: `make check REFERENCE=1 RUN='^TestReferenceDeparseRelations(NativeControls|PreparedLifecycle|ReconstructionConsumers)$'`.
Failed/canceled Restore leaves data/catalogs and held eager tree/result snapshots intact.

Identity Dump emits current generation and actual sequence names inline, skips
duplicate sequence creation/AUTO ownership, and restores literal rows with SYSTEM
override so replay never allocates identity values. Cache reservation high-water and
last/is_called persist; unused session tails and currval/lastval do not. When direct
ALTER SEQUENCE has diverged from the column type or persistence, replay first creates
a valid column-type identity, then applies generated numeric/persistence configuration
before final setval. Reciprocal ownership is remapped under retained TEMP collisions.
Malformed configuration/rows/state or canceled imports leave the old database intact.
CREATE and Dump never evaluate CHECK definitions, including CHECK calls to a
newly declared identity sequence. Restoring literal SYSTEM rows bypasses identity
generation, but ordinary CHECK validation can still call nextval; final setval
restores the captured sequence state. Owner-table collisions with retained TEMP
sequences preserve this replay ordering through Dump/Restore/redump.
Feature smoke: `make check RUN='^TestIdentityCheckControlsSmoke$'`.


Timezone-aware declarations, precision, frozen defaults, NULLs, BC and infinities
use the existing canonical SQL replay. TIMESTAMPTZ literals are ISO UTC with an
explicit +00; TIMETZ literals retain clock and stored fixed offset. Dump is
independent of source TimeZone/DateStyle and does not serialize session settings.
Restore preserves target live settings. Old cursors retain their captured
presentation and metadata after replacement; later invalid temporal input,
unknown zones, I/O errors or cancellation never publish a partial catalog.
Supported closed Stable temporal defaults retain their unevaluated expression
definitions in empty and populated tables: Dump never replaces `AT LOCAL`,
session-dependent casts, comparisons, CASE or NULLIF with source-session results.
Restored rows retain their stored values; subsequent omitted/per-cell defaults
evaluate the restored definition using the destination INSERT's captured settings.
Explicit values (including NULL) still bypass defaults. Frozen typed literal
leaves remain canonical values, distinct from session-dependent operations above them.
This is native logical fixture replay, not PostgreSQL physical backup.

Dump excludes TEMP sequences and records persistent sequence options, owner,
comment, OWNED BY association and generator high-water/called state. Restore
installs new persistent generator identities atomically; it does not serialize
session currval/lastval or unused cache tails. Reset/Restore preserve exact live
caller-owned TEMP sequences and their physical-session state, while persistent
prepared analysis retains its separate catalog-epoch fence. Sequence row results
already returned remain immutable. Native log_cnt is always zero: no controls
promise physical WAL, crash recovery or cache durability.

The internal native catalog-fixture replacement seam imports only a persistent
source projection. Repeated self replacement preserves destination live TEMP
once; foreign or retired source TEMP is never imported or resurrected.
Destination TEMP identity, builtin type dependencies, generator, local values
and cache remain exact. Persistent metadata and detached values use coherent
type identities without mutating retained source catalogs or rows. Public Restore
still rejects TEMP declarations rather than importing them.

`make check RUN='^TestCatalogOwnersPublicWitness$'` additionally checks two opaque DSNs with two simultaneously open pools and held physical connections per instance. It verifies qualified/unqualified INT4 results, same-instance sharing, independent-instance isolation, another instance's Reset/Restore leaving a held cursor unchanged, same-Driver reconnect retention and independent-Driver isolation. Go test reports the result.

Whole-root control migration removes obsolete SQL helpers without changing these
public APIs. Cohesive control/retention witnesses run in both modes:

```sh
make check RUN='^TestReference(Control[A-Za-z0-9_]*|CatalogControlsReleaseLocksDuringIO|DumpRoundTrip|QueryCoreCapturedOwnership|ViewReplaceNativeControls)$'
make check REFERENCE=1 RUN='^TestReference(Control[A-Za-z0-9_]*|CatalogControlsReleaseLocksDuringIO|DumpRoundTrip|QueryCoreCapturedOwnership|ViewReplaceNativeControls)$'
```

Native I/O/cancellation/reader/writer errors, repeated owner-bearing replay and
retained eager cursors use the actual unwrapped public pool. PostgreSQL has no
host Reset/Restore API: its independent SQL consumer/transaction/cancellation
phases drain active streams before subsequent commands on the same connection.
Captured metadata and data remain checked, not merely resource-readiness flags.

Fresh source and recorded phases reuse the complete replay corpora. The source
phase keeps actual setup/append DB.Exec and EACH failed script's DB.Query
sentinel/absence check, while retained cursors are opened before replacement.
Helper I/O gates preserve original reader/writer error identity, cancellation
and worker joins. Roll back borrowed transactions before returning connections,
including failure exits; register schema/role teardown immediately after actual
creation. Do not issue unsupported native cleanup SQL or hide cleanup errors.



## Stored generated columns

Dump serializes `GENERATED ALWAYS AS (...) STORED` structurally, without evaluating
generation expressions. Every generated cell is emitted as DEFAULT, never as an
explicit computed value or identity-override workaround. All-generated rows still
produce one INSERT each. Restore uses ordinary CREATE/INSERT binding and stored
computation, retaining generated NULL definitions and subsequent INSERT/UPDATE
semantics. tableoid references retain their owner ObjectRef; under a retained
TEMP identity collision, replacement recomputes only owner-dependent generated
cells against the remapped final owner and validates completed values before
publication/index installation. Source/TEMP vectors and held eager cursors stay
immutable. Invalid definitions, explicit generated writes, arithmetic/constraint
errors, context cancellation and I/O failures preserve the destination catalog
and data. These are native logical controls, not a PostgreSQL backup/control API.
Owner-remap validation uses a genuine isolated statement/session, preserving
Restore's private owner directives and settings. Session, inquiry, random and
sequence CHECK providers run without the target catalog mutex; their effects
remain private until successful installation. Replacement admission stays held,
and changed target catalog/role/TEMP authority fails without retry. A failed
repair discards private sequence effects as well as repaired cells.


## REGTYPE references and row identity

Reset initializes pg_catalog, public and information_schema type namespaces.
The complete builtin inventory is immutable identity authority, not user objects
to serialize. Real table/view row types and associated arrays reconstruct through
their owning CREATE TABLE/VIEW, not artificial CREATE TYPE statements.

Stored REGTYPE constants retain actual TypeClassID dependencies independently
of their declared datatype. Known numeric constants also retain those edges;
zero and unknown IDs do not. Ordinary stored data alone adds no drop dependency.
RESTRICT/CASCADE follows the existing stored-expression/type closure, including
dependents of an owning relation's row type.

Dump writes actual qualified, quoted scalar referent names and recursively
qualified `element[]` for reciprocal associated-array REGTYPE constants, rather
than source OIDs, generated array aliases or search-path-dependent display names.
Logical replay can remint OIDs and generated names without changing exact logical
type identity, reciprocal relationships, values, defaults or views. Historical
generated aliases are not guaranteed; live catalog names and retained snapshots
remain exact. Explicit named-array domain bases keep dimensions zero: transient
domain/enum replay-name prediction uses the shared allocator and baseline builtin
identities to render the alias available at that declaration, not a history recipe.
Row-type references impose owning-relation topology. Legal default-only cycles
use deferred ALTER COLUMN SET DEFAULT after referents exist; CHECK/generated
and view dependencies remain mandatory topology edges.
Restore parses/binds these real statements in its private catalog, then remaps
scalar/domain/array/stored leaves with TypeClassID addresses. A same-number
RelationClassID mapping never retargets a REGTYPE value. Row/array ownership,
namespace and datatype pointers move together without mutating source snapshots.
Invalid declarations, bad type input, trailing SQL and cancellation preserve
destination state through the existing atomic replacement path.

Feature controls run in both modes (900-second deadline per invocation):

```sh
make check RUN='^TestReferenceRegtype'
make check REFERENCE=1 RUN='^TestReferenceRegtype'
```

