# Native fixture controls

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. They are **host-authorized**, independently of a borrowed connection's effective principal. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity, cluster roles, nonreuse counters, live identities/settings and existing pools. Dump captures before external I/O, groups deterministically by owner name then schema/relation, and writes quoted owner-switch directives plus PostgreSQL-compatible CREATE/INSERT SQL, declaration-ordered columns and canonical literals. Bootstrap-only dumps retain legacy CREATE/INSERT bytes without a redundant switch. Explicit relation/column ACLs cannot be serialized and cause a failure before output, rather than silent loss. Dump excludes role creation, passwords, membership and settings; it is not complete `pg_dump` output, COPY, or catalog export.

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
Dump orders views by their actual persistent source/dependency topology, using
OID only to break ties between independent ready views. Retained TEMP collision
remapping may move a source OID above its dependent without changing replay order.
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
defaults, CREATE SEQUENCE, supported parameter-free scalar/default-request INSERT
(including integer arithmetic), database-local COMMENT, generated sequence OWNED BY
associations, SELECT setval(name,value,is_called), and named SET SESSION AUTHORIZATION
owner directives. Parameters are recursively rejected, and other SQL remains
excluded. Legacy CREATE/INSERT-only scripts remain bootstrap-owned. Referenced
roles must already exist in the target engine. Restore captures a private role
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
The shared object allocator skips live namespace and relation identities imported
by Restore, so subsequent schema and table creation continues without collisions.
Replacement does not rewind the destination's nonreuse counter.

Timezone-aware declarations, precision, frozen defaults, NULLs, BC and infinities
use the existing canonical SQL replay. TIMESTAMPTZ literals are ISO UTC with an
explicit +00; TIMETZ literals retain clock and stored fixed offset. Dump is
independent of source TimeZone/DateStyle and does not serialize session settings.
Restore preserves target live settings. Old cursors retain their captured
presentation and metadata after replacement; later invalid temporal input,
unknown zones, I/O errors or cancellation never publish a partial catalog.
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
