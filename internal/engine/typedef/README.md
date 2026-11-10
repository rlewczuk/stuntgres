# internal/engine/typedef

User-defined type/domain, collation and conversion decisions, generation and
reownership helpers below core. It consumes catalog/DDL and typed expression
owners, never core or the root, and owns no engine state or mutex.

## What it owns

Type/companion definition stagers; ALTER resolvers; domain rule/program records;
enum/range/composite evolution; typed-table decisions; scalar import/remap;
canonical type dump/replay helpers and engine-owned stage/journal slot records.
Input/output and executable provider capability remain independently authenticated.

## Type identity lifecycle

Logical type identity, immutable executable definition and analysis stamp are
separate. Pure identity edits preserve logical values and eligible stamps;
semantic mutations invalidate through their owning generation rules. Surviving
companions and concrete members retain genuine exact-generation authority.
Removal/recreation never retargets old values by same name or numeric OID.

## The stage host

`StageHost` embeds `ddl.Host` and type staging capabilities. Opaque authority is
kept by value; retargeting returns a new view. Every metadata host method runs
under caller-owned Engine.Mu and neither reacquires it nor waits. Private Stage
begin/discard/validation/publication belongs to the engine.

## Invariants

- Read only explicit catalog/stage inputs. Locked resolvers/import/retention
  decisions take no lock of their own. Replay leaf callbacks that bind expressions
  run outside Engine.Mu through the real supplied adapter.
- Engine-owned slots use nil for absent, checked recovery and COW replacement
  before mutation; savepoints retain old backing.
- Seed constructors return fresh mutable descriptors or explicitly shared
  read-only parsed definitions. Installation order fixes released identities,
  not expected dynamic allocation sequences.
- Domain/type evolution retains graph/storage/provider/owner/epoch observations
  through one publication. Historical row/index work runs detached; cancellation
  or stale final authority installs no prefix.
- Native typed replay validates actual destination usages and authenticated import
  capabilities. Structural metadata, row-sidecar provenance and normal scalar
  SQL admission are distinct and cannot authorize one another.

## What stays in the engine

`typecmds` and schemacmds own utility orchestration; tablecmds supplies shared
hosts. Core owns domain checks, generation coordination, row/index preparation,
physical-session TEMP retention, provider callbacks and final publication.

## Testing

Pure seed/stage/identity/replay units live here. Core/family fixtures cover real
row generation, remap and publication ownership; public SQL belongs to reference
type/domain packages under [docs/TESTING.md](../../../docs/TESTING.md).
