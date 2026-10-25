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
`AcquireTypedSources`) and the decision packages, never the root. Only the
root composition imports it; by rule 6 `viewcmds` may, for the domain replay
that the materialized snapshot import shares.

Stays elsewhere: CREATE DOMAIN, the other CREATE TYPE forms, CREATE
COLLATION and CONVERSION, and ALTER DOMAIN RENAME and SET SCHEMA dispatch
through the schema statements (still in the root). `TypeHost` is
`tablecmds`' because those build it too. Domain checks on the row path
(`StatementScope.CheckDomain`), domain input decoding, domain check
programs, type generations and TEMP collation cleanup are core's.

## Entry points

- Dispatch (`core.Statements.Execute`): `ExecuteAlterDomain`,
  `ExecuteAlterComposite` and `ExecuteEnumDDL` are entered with nothing held.
  They resolve under `Engine.Mu`, wait for the type and table locks
  unlocked, recheck the resolved descriptors after the waits, and stage,
  validate and publish one catalog stage.
- Dispatch (`core.Statements.ExecuteLocked`): `AlterTableOfLocked` is a
  lock-domain continuation: entered with `Engine.Mu` held and the table
  admitted, it releases it on every return.
- Dump: `ValidateTemporaryCollationDump` fails a Dump when an object outside
  a temporary namespace depends on a temporary collation.
- Restore hooks: `ExecuteDomainReplayInsert` runs a private Restore INSERT
  paired with a native domain replay record; `CaptureRestoredTypeOwner` and
  `RestoreTypeOwner` pair a created type with its captured owner. Each
  admits only a trusted private import.
- Shared with the view family: `CaptureDomainReplay` and `DomainReplayEnv`.
- For the root tests that stage through the schema statements: `TypedStage`,
  `TypedMutation` and `TypedRelationMutation`.

## Testing

Tests are external (`package typecmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. The ALTER DOMAIN, composite, enum and
domain replay units that stage through the root's schema statements stay in
the root until those move. Public SQL lives in
[reference/domain_types](../../../reference/domain_types/README.md#testing)
and [reference/types](../../../reference/types/README.md#testing).

```sh
make check RUN='^TestTyped'                                # typed tables: ALTER TYPE rows, publication fences, admission
make check RUN='^Test(GeometryConversionReplay|DomainDefaultReplay)'  # native domain replay
make check RUN='^Test(DomainAlter|CompositeAlter|CompositeDomainArray|EnumInvalidLabel)'  # root units of these executors
make check RACE=1 RUN='^TestTyped(Publication|Admission)'  # typed publication and admission races
```
