# reference/triggers

Public dual-mode event-trigger scenarios cover database-local declarations,
function frames and identity, command-phase ordering, private DDL publication,
actual object/subobject drops, semantic table rewrites, target-sensitive utility
eligibility, ownership, disabled state, transaction recovery and held statements.
Dedicated catalog scenarios check the full schema on actual empty sources,
definition/owner/private-function identity, nonowner visibility and held-query
freshness across transactional publication without executing referenced functions.
Native-control phases exercise committed capture, final-section activation,
two replacement rounds, fresh-driver remapping, unchanged-stream PostgreSQL
replay and failed replacement preservation.
Demoted-owner portable Dump rejection preserves the live descriptor and writes
no prefix. The generated-column rewrite witness uses existing SET EXPRESSION;
unsupported ADD COLUMN generated syntax is not a prerequisite expansion.
Journaled concurrent materialized refresh is covered; independently published
concurrent index creation is outside this one-journal callback boundary.
Its concurrent-refresh witness filters TAG to REFRESH MATERIALIZED VIEW and
requires the exact outer start/end pair on both backends; the other relation
commands retain unrestricted callbacks. Maintenance exclusions exercise shipped
VACUUM; standalone ANALYZE is not an admitted native statement.
Standalone-only procedure/language creation retains its existing admission.
Inherited and typed-table ALTERs expose all new declared shapes before any
per-relation rewrite callback. Callback-created objects and unrelated ALTERs
share the same private outcome; failure preserves old metadata and data.
Direct USING-column references and no-op casts do not notify table_rewrite;
an arithmetic USING expression does, even when its values happen to be equal.
Typmod and domain conversions assert exact callback multiplicity, converted
values, driver types and installed column types through direct and prepared
ALTERs. Timestamp conversions exercise both directions in UTC and in a zone
with historical nonzero offsets, including values whose modern offset is zero.
TG text datums are witnessed by independent text/varchar overload selection,
not an unsupported type-inquiry builtin. Function replacement preserves explicit
EXECUTE grants and denials as exercised by real role-switched calls; OID/owner
snapshots do not depend on an unshipped catalog ACL column. Persistence captures
the actual fixture creator's role name and carries it through unchanged replay.
Rewrite filters retain the tested ALTER TYPE/TABLE tag, and audit helpers use
qualified public identities so restricted-search-path concurrent refresh remains
an actual callback witness.

All scenarios use the shared `reference/fixture`; no private engine interfaces or
alternate harness are used. `eventTriggerScenario` owns the configured-backend
loop and registers database-global cleanup before declarations. Each backend and
replay owner retires triggers under a fresh bounded context before schema
teardown. Failure callbacks must never survive into fixture reset. Audit helpers
use fixed phase values rather than transactional assumptions about sequences.
Real lock-admission observers establish pre-publication peer witnesses without
sleeps or simulated callback barriers.

`event_trigger_dropped_helpers_test.go` owns dropped-record audit setup and
independent complete logical bags. Generated addresses and RI-trigger names are
captured from public catalogs before deletion; expected kinds, identities,
provenance, temporary ownership and multiplicities are not learned from helper
output. The dropped-record scenarios retain both reads, phase/catalog-absence
witnesses, live-helper metadata, full protocol diagnostics and native-control
roundtrips with unchanged-stream PostgreSQL replay.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` delegates directly to
`fixture.Main`. Existing ordinary DML-trigger scenarios remain in
[reference/operators](../operators/README.md#testing).

Event-trigger family selector: `RUN='^TestReference(EventTrigger|PGEventTriggerCatalog)'`. Add
`REFERENCE=1` for the pinned PostgreSQL oracle. Direct, fresh-prepared and held
utility handles share the same SQL definitions and independent expected rows,
metadata, diagnostics and atomicity assertions.
