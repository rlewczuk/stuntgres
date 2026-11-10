# internal/engine/sequence

Sequence options/limits, descriptor/generator decisions, physical-session memory,
prepared literals, staging and dump formatting below core. The package adds no
engine state, mutex or independent publication path.

## What it owns

Pure option/ownership/identity decisions and relation shape; typed call rules;
`SessionMemory`; immutable prepared analysis/observations; stage generator
replacements; control-copy and canonical dump values.

## Rules

Catalog definitions, nontransactional generator effects and physical-session
currval/lastval memory are distinct owners. Sequence DDL reuses catalog ACLs,
transaction stages and canonical relation admission; function demand uses the
current engine host rather than a retained permission or source name.

Generators retain monotonic reservation/tail ownership. Rollback of a definition
or savepoint does not invent counter undo, while failed private publication
cannot install a replacement generator. Copies/remint use genuine source and
destination authority, not equal OIDs or value tails.

## Prepared sequence literals

Prepared captures retain exact source occurrences and original sequence identity
through normal analysis. The description owns its private cell/observation;
compatible metadata and executable generation invalidation remain distinct.
Current namespace/ACL and execution fences are independent of literal retention.
No observation retains an engine callback, session grant or mutable generator.

## What stays in the engine

`tablecmds` owns utility orchestration; core owns session memory access, catalog
and epoch fences, lock waits, current authority, generator installation and
transaction completion. `storage` owns physical generator state.

## Testing

Pure option/memory/call/analysis units live here. Core/family tests cover actual
session, prepared and publication lifetime; public SQL belongs to
`reference/sequences` under [docs/TESTING.md](../../../docs/TESTING.md).
