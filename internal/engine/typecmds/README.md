# internal/engine/typecmds

The type statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): ALTER
DOMAIN, ALTER TYPE on composite types with the typed tables and inheriting
children it rewrites, CREATE TYPE ... AS ENUM and AS RANGE, ALTER TYPE on
enums, ALTER TABLE ... OF and NOT OF, the native domain replay, and the type
Dump and Restore checks. The decisions are
[`typedef`](../typedef/README.md)'s and [`table`](../table/README.md)'s;
this package orchestrates them over core's runtime.

## Layering

It imports [`core`](../core/README.md), [`tablecmds`](../tablecmds/README.md)
(`TypeHost` and `TableHost`, the ALTER column candidates, row evaluation,
check and index captures and type rebinds, `PrepareAlterTypeLocked` and
`AcquireTypedSources`) and the decision packages, never the root. The root
composition and, by rule 6, [`viewcmds`](../viewcmds/README.md) import it;
`viewcmds` for the domain replay that the materialized snapshot import
shares.

Stays elsewhere: CREATE DOMAIN, the other CREATE TYPE forms, CREATE
COLLATION and CONVERSION, and ALTER DOMAIN RENAME and SET SCHEMA dispatch
through the schema statements ([`schemacmds`](../schemacmds/README.md)).
`TypeHost` is `tablecmds`' because those build it too. Domain checks on the row path
(`StatementScope.CheckDomain`), domain input decoding, domain check
programs, type generations and TEMP collation cleanup are core's.

Existing-value domain validation and typed row/index preparation activate only
in detached runtime phases. Typed scans and layout transport reuse the invocation
context rather than activating per row. Domain replay capture remains dormant
under its capture mutex; subsequent real row execution owns runtime admission.
Typed-table index rebuilds pass the actual transformed table's restricted
maintenance-owner callback to the shared late index selector, just as table
ALTER does. Only selected builds enter that environment; initiating disclosure,
historical row work, produced-entry reuse and final publication fences remain
separate. Composite/source-type ownership never substitutes for table ownership.

Enum generation adoption carries the heap candidate's observed VM root and
actual preserving-copy provenance into the transaction journal without replacing
its earlier authoritative observation. Typed mutation mints that provenance
only when codec reowning copies the unchanged observed heap; the copied root and
VM observation are sampled in the same `Engine.Mu` window. A projected/rewrite
candidate starts its own modeled VM and cannot regain committed-predecessor
continuity merely because a later enum generation transports its datums.

## Entry points

- Dispatch (`core.Statements.Execute`): `ExecuteAlterDomain`,
  `ExecuteAlterComposite` and `ExecuteEnumDDL` are entered with nothing held.
  They resolve under `Engine.Mu`, wait for the type and table locks
  unlocked, recheck the resolved descriptors after the waits, and stage,
  validate and publish one catalog stage.
  Typed cascades pass each stored table's checked TYPE reason mask to the same
  unlocked physical callback boundary; the inspected OID is the table, never the
  composite type, and the enclosing command tag remains ALTER TYPE.
- Dispatch (`core.Statements.ExecuteLocked`): `AlterTableOfLocked` is a
  lock-domain continuation: entered with `Engine.Mu` held and the table
  admitted, it releases it on every return.
- Dump: `ValidateTemporaryCollationDump` fails a Dump when an object outside
  a temporary namespace depends on a temporary collation.
- Restore hooks: `ExecuteDomainReplayInsert` runs a private Restore INSERT
  paired with a native domain/seg replay record; `CaptureRestoredTypeOwner` and
  `RestoreTypeOwner` pair a created type with its captured owner. Each
  admits only a trusted private import.
  Seg replay additionally validates the genuine destination provider and exact
  canonical literal. Reinputtable values bind independently; derived infinity
  and inverted endpoints use only the checked private typed substitution.
- Shared with the view family: `CaptureDomainReplay` and `DomainReplayEnv`.
- For the `schemacmds` tests that stage through the schema statements and the
  root's typed-table fixture: `TypedStage`, `TypedMutation` and
  `TypedRelationMutation`.

## Testing

Tests are external (`package typecmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. The ALTER DOMAIN, composite, enum and
domain replay units that stage through the schema statements live in
[`schemacmds`](../schemacmds/README.md#testing). Public SQL lives in
[reference/domain_types](../../../reference/domain_types/README.md#testing)
and [reference/types](../../../reference/types/README.md#testing).

`TestPgvisibilityCheckTypedMutationProducer` consumes genuinely deferred metadata
before actual typed row production. Its old-codec heap must remain unchanged until
that producer makes the preserving copy; the destructive rewrite control cannot
inherit VM provenance merely from a later codec transport.

The native-private `TestTypedPublicationCallbackReplacement` retains five real
registered-provider routes: ALTER, DROP with a surviving holder, CREATE,
temporary CREATE and attachment. Each child builds its fixture and installs the
delegating provider before READY; an explicit START admits execution. Startup
has a separate 30-second bound. The unchanged 25-second child watchdog and
30-second parent operation bound then cover actual callback Restore,
`ErrCatalogReplaced`, epoch/descriptor displacement, no old relations, sentinel
91, empty locks, released replacement admission and operation/session teardown.
Phase records diagnose progress; neither READY nor a completion record replaces
the assertions and successful process exit. The operation clock never refreshes.
Every started-child exit cancels and waits, with a 15-second reap bound that
outlasts the 10-second pipe-copy bound; a missed bound is a failure, not a successful cleanup.

`TestTypedCallbackProcessOwnership` independently exercises real-process startup,
admission, early exit, protocol errors, operation/watchdog/assertion failures,
stalled teardown and stalled terminal exit. Pipe-held controls and injected
deadline signals exercise ownership and cancel/reap without sleeping or waiting
out budgets. Injected signals supplement, never replace, real 30-second startup
and operation safety bounds (hang guards sized for a loaded machine); expected injected failures must name their injected
owner, not pass because a real safety deadline eventually expires.

The `phase-copy` control independently streams fragmented records through actual
`io.Copy` while its source pipe remains open, then closes and joins both owners.
The stdout sink uses named storage: embedding `bytes.Buffer` would promote
`ReaderFrom`, allowing `io.Copy` to bypass phase interception and strand admission.
Each diagnostic stream retains at most 1 MiB; excess output explicitly fails
with a capture-limit marker. The same control checks capture and protocol-queue
overflow. None of these controls replaces any genuine callback case.
This harness revision does not identify the cause or phase of the historical
whole-suite DROP kill and does not change engine or SQL semantics.

```sh
make check RUN='^TestTyped'                                # typed tables: ALTER TYPE rows, publication fences, admission
make check RUN='^Test(GeometryConversionReplay|DomainDefaultReplay)'  # native domain replay
make check RUN='^Test(DomainAlter|CompositeAlter|CompositeDomainArray|EnumInvalidLabel)'  # their units in schemacmds
make check RACE=1 RUN='^TestTyped(Publication|Admission)'  # typed publication and admission races
make check RUN='^TestTyped(PublicationCallbackReplacement|CallbackProcessOwnership)$'
make check RACE=1 RUN='^TestTyped(PublicationCallbackReplacement|CallbackProcessOwnership)$'
```
