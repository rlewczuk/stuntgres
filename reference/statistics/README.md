# Statistics reference scenarios

This bounded feature package owns the public SQL scenarios for the nine-column
`pg_stat_all_indexes` view. All Go tests are untagged external tests using public
stuntgres and `reference/fixture` APIs. The same scenario bodies serve native and
the fixture's pinned PostgreSQL 18.6 oracle; native controls remain explicit
native-only phases, not a simulated PostgreSQL Restore or Reset API.

## Contract

- Actual parent/index/schema identities, independent index OIDs, nullable typed
  metadata, direct/prepared/empty/retained results and raw adapter values. Fresh
  activity is zero counters and SQL NULL `last_idx_scan`, not an invented date.
- Actual access ownership authenticated by EXPLAIN: native hash hit/miss and
  lossy bulk bloom, PostgreSQL simple/bitmap/index-only behavior and actual
  `Heap Fetches`. Candidate entries are not inferred from output cardinality.
  Native B-tree presence creates no new indexed access method.
- Negative access witnesses explicitly enable sequential scans and disable
  index paths before checking the actual Full/Seq Scan plan. Disabled
  collection and prepare-only/NULL cases do not fabricate access events.
- Complete read-only diagnostics, including diagnostic fields and
  prepare-versus-execute boundaries, unchanged data/identity/history, COPY
  failure without affected rows or a command tag, and subsequent usable SELECT.
- Real parent-schema denial does not redact view identities/counters. The view
  retains its own current SELECT ACL, including already-held WHERE FALSE
  statements. Absence of activity and privacy redaction are not conflated.
- Shared none/cache/snapshot cuts, clear without publication/reset, actual
  writer retirement and independent bounded publication witnesses. Drop,
  rollback, rename/move and same-name recreation preserve the correct identity
  and generation boundaries.
- Transaction-stop timestamps, rollback-surviving read work and preparer-owned
  PREPARE publication. Native explicit/private queries retain their existing
  full-scan fallback; exact positive implicit stop/abort clocks and interrupted
  prefixes are covered by composed core ownership tests.
- Actual native Dump/Restore/Reset, failed/canceled Restore atomicity and
  destination-attached TEMP index history. PostgreSQL replays the actual emitted
  definitions/data and independently checks fresh activity. Runtime statistics
  are never serialized. Retained rows compare every identity/name/counter,
  NULL validity and exact timestamp instant, not Go time-zone pointer identity.

Common lifecycle bodies live in `fixture/statistics_helpers.go`; index-specific
row and expectation helpers remain local to this package. Public physical
retirement is a genuine terminal publication boundary, not a new SQL flush API.

## Testing

Run all nine definitions in native and real pinned PostgreSQL modes:

```sh
make check RUN='^TestReferencePGStatAllIndexes(IdentityAndMetadata|Events|AccessOwnership|ReadOnly|Privileges|SnapshotsAndLifetime|TransactionStop|NativeControls|FeatureSmoke)$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllIndexes(IdentityAndMetadata|Events|AccessOwnership|ReadOnly|Privileges|SnapshotsAndLifetime|TransactionStop|NativeControls|FeatureSmoke)$'
```

The feature smoke uses a real hash hit and held-statement miss, exact identities
and typed metadata, an authenticated sequential negative, a B-tree control,
read-only diagnostics and committed drop absence. Native hit plus miss produces
2 searches / 2 entries / 2 simple fetches with a non-NULL last scan; PostgreSQL
expectations follow its independently authenticated access method.

```sh
make check RUN='^TestReferencePGStatAllIndexesFeatureSmoke$'
make check REFERENCE=1 RUN='^TestReferencePGStatAllIndexesFeatureSmoke$'
```

Shared-helper changes also require the originating session-state consumers:

```sh
make check RUN='^TestReference(SessionActivity(Values|PreparedAndBatch)|StatsSnapshot(Lifecycle|Modes|Invalidation|TransactionReporting|PreparedReporting|TruncateReporting|AbortReporting))$'
make check REFERENCE=1 RUN='^TestReference(SessionActivity(Values|PreparedAndBatch)|StatsSnapshot(Lifecycle|Modes|Invalidation|TransactionReporting|PreparedReporting|TruncateReporting|AbortReporting))$'
```

Composed interrupted-prefix proof, including race mode, uses
`make check RUN='^TestPGStatAllIndexesInterruptedAccess$'` and the same command
with `RACE=1`. Whole-project Docker-free verification remains `make quickcheck`.
