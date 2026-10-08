# Native fixture controls

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

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

Persistent view definitions are dumped after their relation dependencies, with
fixed output names, current bound relation identities, frozen column namespace labels, security options and
owner directives. Views are not evaluated by Dump/Restore and their output is
never serialized as table rows. Restore binds canonical CREATE VIEW in its
isolated catalog, remaps every stored source/expression/dependency identity into
the destination generation, and atomically installs definitions with ordinary
data. Derived catalog sources initialize normally and are not dumped. Restored
views remain dynamic; later base writes and dependency checks still apply.
Failed/canceled imports retain old catalogs, dependencies and held result rows.
Dump orders tables and views through their persistent provider topology.
Views depend on table/view sources and expression providers; tables depend on
their CHECK providers. An identity-sequence dependency waits for the owning table
whose inline declaration creates it. A table's own identity CHECK is not a
self-edge; ordinary standalone sequences remain in the earlier creation prefix.
Creation addresses break ties between ready objects. Retained TEMP collision
remapping may move a source OID above its dependent; topology then wins so replay
remains executable, without promising allocator-relative diagnostic-order parity.
The two-retained-sequence, second-Dump/Restore regression is executable with
`make test PACKAGES=. RUN='^TestViewNativeNestedSecondDumpAfterTemporaryRemap$' VERBOSE=0`.
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
Dump/Restore, including NUMERIC display scale. Public direct/prepared roundtrip
coverage: `make test PACKAGES=. RUN='^TestViewContextualScalarLiteralNativeRoundTrip$' VERBOSE=0`.

Restore accepts supported CREATE/ALTER/DROP SCHEMA, CREATE TABLE with supported
defaults and identity definitions, CREATE SEQUENCE, supported parameter-free
scalar/default-request INSERT (including integer arithmetic and SYSTEM identity
override), database-local COMMENT, generated sequence OWNED BY associations,
numeric/persistence configuration of internal identity sequences, SELECT setval(name,value,is_called),
and named SET SESSION AUTHORIZATION
owner directives. Parameters are recursively rejected, and other SQL remains
excluded. Legacy CREATE/INSERT-only scripts remain bootstrap-owned.

Selected parameter-free btrim/ltrim/rtrim, normalize and is_normalized calls,
including their already-admitted special forms and nested string concatenation,
share ordinary scalar execution. Restore validates every argument: arbitrary
FunctionExpr producers (including random/VOID effects) remain excluded. BOOL
VALUES use the existing ordinary is_normalized function route, not new IS-predicate
VALUES syntax. Bad normalization forms reach the shared 22023 provider error,
and failed imports preserve old data/catalogs and held cursors.
Stored string DEFAULT/CHECK expressions retain special versus ordinary syntax,
omitted versus explicit normalization forms, and recursive structural/deparse
carriers through native replay without evaluating definitions during Dump.
Ordinary string calls display their resolved TEXT conversions without changing
runtime arguments; special syntax hides implicit conversions. Explicit-set special
BYTEA TRIM CHECKs preserve direction and spelling, including nested consumers and
retained carriers. This does not admit BYTEA DEFAULTs or additional public casts.
Captured NAME literals inside string arguments retain an explicit admitted NAME
annotation during replay, rather than becoming unknown literals resolved as TEXT.

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
The shared object allocator skips live namespace, relation, view-rule, CHECK,
NOT NULL and default identities imported by Restore. Replacement does not rewind
the destination's nonreuse counter. Definition collisions with retained native
objects are reminted in the private candidate before installation.
Dump/Restore preserves CHECK casts/names/NO INHERIT, NOT NULL and explicit
defaults through canonical SQL replay, not raw definition OID directives.
Restored IDs may differ; current owner/attribute relationships and behavior persist.
Structural unknown-NULL provenance and raw temporal precision survive replay;
destination sizing remains an executable coercion rather than a deparse-time value.
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
Plain TIME AT and NAME[] equality/order remain outside the admitted surface.
Identity consumers cover all admitted keyword/quoted/qualified call forms and
actual returned DEFAULT/CHECK copies through authority and ordered/missing paths.
Native NULL SYSTEM_USER and PostgreSQL SCRAM identity are independent expectations.
Native replay fixtures keep context-sensitive CHECK objects empty, retain literal
rows in separate default-only objects, then exercise symbolic definitions through
caller-held post-Restore DML. Replay uses its existing isolated context, not the
caller's identity/path. Actor-owned schema setup and CREATE under SET ROLE use
existing ownership routes rather than adding GRANT or ALTER TABLE OWNER support.
Changed-type default fixtures require a fresh own definition OID, unchanged
relation/attribute identity and absent old live lookup; eager original metadata
remains usable. Actual copied default text produces the expected BIGINT value,
while failed conversion preserves the old definition, type and complete rows.
Additional metadata witnesses retain signed INT4 minimum constants, typed NULL
arguments, raw explicit TIME(3) precision, and quote/backslash spelling under both
standard_conforming_strings settings. Actual copied NUMERIC(2,-1) defaults retain
149/995 before assignment: 149 rounds to 150; 995 overflows at Execute, with no
partial row prefix. Native replay asserts the same stored structure and behavior.
Derived pg_constraint/pg_attrdef rows and stateless deparse functions are not dumped.
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
Feature smoke: `make smoke RUN='^TestIdentityCheckControlsSmoke$' VERBOSE=0`.


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

`go test -count=1 -run '^TestCatalogOwnersPublicWitness$' ./smoke` additionally checks two opaque DSNs with two simultaneously open pools and held physical connections per instance. It verifies qualified/unqualified INT4 results, same-instance sharing, independent-instance isolation, another instance's Reset/Restore leaving a held cursor unchanged, same-Driver reconnect retention and independent-Driver isolation. Go test reports the result.
