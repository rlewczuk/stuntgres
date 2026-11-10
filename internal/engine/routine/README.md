# internal/engine/routine

Routine/operator inventories, catalog-aware support binding, callable/cast/
aggregate DDL decisions and declaration/dump presentation below core. Mutable
invocation state belongs to proc/exec/core, not inventory records.

## Routine ALTER contract

Routine identity changes and attribute patches stage real descriptors through the
ordinary DDL journal. Resolve exact namespace/input identity and canonical kind;
check actual ownership and the relevant namespace authority before staging.
A metadata change is not permission to invoke a routine or reinterpret its body.

Checked stored calls retain selected identities and manifests. Quoted bodies keep
soft original source for ordinary later resolution. Replacement compatibility
checks declaration-owned result/set/formal facts independently of current body
admission. Pure identity edits and executable changes retain distinct generation
and invalidation rules.

## What it owns

- Immutable builtin/operator declaration lookup and fresh seed constructors.
- One catalog-aware index-support binding path using actual method/class/family
  identities and genuine selected routine signatures.
- Routine/operator/cast/aggregate candidate decisions and shared utility selection.
- Declaration normalization, input/default/result compatibility and metadata-safe
  presentation. Display/source facts are not executable capabilities.
- Canonical symbolic dump SQL and replay definition validation.

## Invariants

Inventories and indexes are built once and read-only; borrowed pointers must not
be modified. Seed constructors return fresh descriptors in fixed installation
order with authentic provider metadata. Mutable names/owners/membership do not
replace sealed declaration provenance.

Metadata-only declarations are not executable by virtue of catalog presence.
Language admission, SQL/PL body validation, routine EXECUTE, aggregate support and
provider invocation remain separate boundaries. Return shape, set cardinality
and member generations are independently checked.

The package owns no mutex or engine state. Reads use explicit catalog/DDL hosts;
canonical SQL and diagnostic/error-order behavior are compatibility contracts.

## The DDL host

`DDLHost` embeds `ddl.Host`. Handlers run under caller-owned Engine.Mu with no
statement scope; Host methods do not reacquire it or wait. Opaque Authority is
kept by value and changed views return copies. Stage is private; its owner
controls begin/discard/validation/publication and unlocked execution work.

## What stays in the engine

`schemacmds` owns RoutineHost and statement orchestration. Core owns actual body
invocation, environments, session specialization, current authority and atomic
publication; `proc` owns immutable programs/private frames.

## Testing

Pure inventory/selection/normalization/compatibility units live here. Composed
core/family fixtures cover real staging and retained execution ownership; public
SQL belongs to reference routine/operator packages under
[docs/TESTING.md](../../../docs/TESTING.md).
