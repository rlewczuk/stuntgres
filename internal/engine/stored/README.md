# internal/engine/stored

Frozen SQL definitions, recipes and presentation: immutable catalog payloads for
views, rules, policies, triggers and routine programs; deparse, dependency walks,
retagging and destination reownership. It sits above analysis/regobj and never
imports core or owns a live session, mutex or publication.

## What it owns

- Payloads expose private backing through checked constructors/Parts/With APIs.
  Definitions retain typed source/target occurrences and selected dependencies,
  not mutable execution frames or retained grants.
- `RoutineProgram` retains checked SQL-standard actions and defining manifests;
  quoted source and declaration-only recipe text remain separate authority.
- Deparser/rendering owners present expressions and queries without evaluation.
  Callers supply current captured names/path/output and bound column context;
  renderers never reconstruct invocation state from SQL text.
- Retag/remap walkers copy changed structure and share unchanged immutable backing.
  Address mapping, full reference mapping, principal mapping and type-generation
  mapping are distinct contracts.
- Explicit routine publication/transport helpers authenticate actual predecessor
  and selected recipe provenance before producing candidates. Metadata changes
  do not silently rebuild physical produced keys or replay earlier row effects.
- Catalog-edge visitors and full executable manifests are separate projections.
  Definition display can remain valid when current executable admission fails.

## Recipe rules

Store the selected recipe, not merely source/result endpoints. Conversions,
written/formal operand order, source occurrences, collation/type generations,
provider identity and analyzed dependencies survive pruning and remapping.
Expression protocol substitution must not reselect a provider or invent new
source association. Installed wrappers authenticate genuine sealed declarations,
not current spelling or extension membership alone.

Historical recipe remapping preserves genuine original declarations/manifests;
current-compatible reownership requires independently validated destination
identity. Names, OIDs and structural payload equality are insufficient. TEMP
retention follows actual destination ownership, not imported source resources.

## Invariants

- Published payloads and source expressions are immutable. Constructors, With,
  retag and remap return new values and never mutate retained backing.
- Rendering and canonical dump SQL are byte-stable compatibility contracts.
  Display and replay validation are separate: presentation grants no execution
  or private import capability.
- Mapping preserves property/dependency-purpose rules even when executable
  branches disappear. Retained folded values do not erase analyzed authority.
- ProcedureProvider/TypeProvider are canonical validators with defined error
  order. Runtime admission remains independent of structural metadata validation.
- Every function consumes explicit captured reads/session identity/arguments.
  Nothing caches permissions or consults a hidden live engine.

## What stays in the engine

DDL/refresh execution, source capture, actual routine invocation and effective
users/settings, stage lifetime, physical index transport, TEMP planning and atomic
publication. `view` owns rule/write/RLS decisions over these frozen payloads.

## Testing

Pure payload/recipe/renderer/retag/remap units live here. Composed core/family
fixtures retain real accepted graphs, generations and backing through publication
failure and owner cleanup. SQL equality is not an ownership assertion; public
SQL belongs to reference packages under [docs/TESTING.md](../../../docs/TESTING.md).
