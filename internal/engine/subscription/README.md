# internal/engine/subscription

Subscription validation and immutable metadata staging below core. `Stage`
consumes positioned syntax through a `ddl.Host` extension; `schemacmds` owns
operation capture, lock admission and publication. This package never takes
locks, publishes roots, changes settings or performs external I/O.

The host enters with the cluster mutex held. Build complete candidates before
replacing descriptors. Publications are ordered remote names, not local catalog
dependencies. Connection parsing validates syntax only, without environment,
service/password files, DNS or sockets; retain the original supplied text.

Desired enabled/activation metadata is not running replication. Operations that
require unavailable publisher/slot/synchronization mechanisms fail explicitly
before publication, rather than creating fake workers, progress or connections.
Core owns unlocked physical-origin waiting/readmission and shared statistics
lifetime; private stages/journals own provisional metadata until commit/discard.

## Testing

Pure option/connection/slot validation units live here, parser syntax with the
parser, and stager/lifecycle tests with core. Public direct/prepared SQL belongs
to `reference/catalogs` under [docs/TESTING.md](../../../docs/TESTING.md).
