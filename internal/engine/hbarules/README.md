# Simulated HBA diagnostic facts

`hbarules` is a leaf owner of independent nullable `Rule` facts and their stable
Driver-wide `Source`. It imports only the standard library and carries no SQL
catalog descriptors, authentication logic, parser, filesystem or network state.
The root driver publicly aliases Rule as HBARule; no public getter exposes backing.

`Source.Set` deep-copies the outer batch, scalar pointees and nonnil string slices
once before publishing one immutable Snapshot. Nil/empty outer slices clear. Nil
scalars/arrays mean SQL NULL; nonnil zero/empty scalars and nonnil empty arrays
remain distinct. All facts, supplied order and duplicates are verbatim.
Callers must not mutate inputs during Set, but may do so after it returns.

The zero Source is empty; nil Source.Capture and nil Snapshot.Rules are empty.
Capture briefly holds an RLock to retain the existing Snapshot pointer, without
cloning facts. Rules returns borrowed immutable internal backing: internal
consumers must not mutate it. Each publication owns distinct backing; retained
snapshots remain valid after later replacement or clear.

Publication is the Set linearization point. Copying happens before the source
lock; source writers never acquire Driver or Engine locks. The driver gets or
creates its holder under Driver.mu and releases that mutex before Set. Query
capture may take Source.RLock under Engine.Mu only after current SELECT and
independent routine EXECUTE admission. Typed materialization occurs unlocked,
using the invocation's current catalog descriptors and newly owned cells/arrays.

Every engine, template seed and logical runtime of a Driver shares the same
holder, allocated even before the first configured facts. Reset and staged
Restore retain the destination holder, not a frozen fact batch or imported
source holder. Dump excludes all facts. Separate Drivers remain independent.

## Testing

External component tests live in `enginetests`; use
`make check RUN='^TestHBARulesSnapshot'` (add `RACE=1` for joined publication
readers/writers). Root adapter ownership tests use `^TestHBARulesAPI`; producer
and composed holder ownership tests use `^TestPGHBAFileRules`. Public SQL outcomes
belong to the shared `reference/catalogs` scenarios, never these pure owner units.
