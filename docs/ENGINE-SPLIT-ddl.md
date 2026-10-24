# Engine split: `internal/engine/ddl`, fifth round

Session prompt for linear item 166 of the
[status page](ENGINE-SPLIT-STATUS.md). The rules are in
[ENGINE-SPLIT.md](ENGINE-SPLIT.md) §2, §3 and §5, and the package contract
is in [the ddl README](../internal/engine/ddl/README.md). Sizes were
measured at `fe0683b68`; re-run `reach` on the move set first.

## Item 166 — CREATE DATABASE decisions

Needs —. About 0.16k lines move. CREATE DATABASE (`database_create.go`)
landed in the root after the round was planned. Its decisions belong
beside the tablespace and LOAD validators in `ddl`. If DROP or ALTER
DATABASE has landed by the time this item starts, take their pure
decisions too.

**Moves.**
- `databaseCreateOptions`, `databaseOptionError` and
  `parseDatabaseCreateOptions`, which `reach` already reports clean, become
  `ddl.CreateDatabaseOptions` and `ddl.ParseCreateDatabaseOptions`.
- The owner decision of `databaseCreateOwnerLocked` becomes a function over
  the role read (`authorization.Read`) and the current user, both captured by
  the root:
  - the owner lookup;
  - the CREATEDB or SUPERUSER check;
  - the `must be able to SET ROLE` rule.
- The admission checks in `executeCreateDatabase`, in today's order:
  - template (`template1`, `template0`, missing, other);
  - encoding;
  - LC_COLLATE and LC_CTYPE;
  - the unsupported-capability option;
  - the duplicate name.

  They run over the parsed options and an `exists(name) bool` callback that
  the root answers from `e.cluster.targets` while it holds `Engine.mu`.

**Stays root.**
- `authorizationLocked` and `Engine.mu`;
- `createDatabaseTarget`, `publishDatabaseTargetLocked`;
- the cluster registry (`database_cluster.go`) and `newDatabaseRuntime`.

**Callers and locks.**
- Option parsing runs with nothing held.
- The owner and admission decisions run under `Engine.mu` and take no lock.

**Risks.** The error order is part of the contract. Options are checked
before actor authorization; then come owner, template, encoding, locale,
capability, duplicate and the context check.

**Tests.** Quick and `REFERENCE=1` selection: `TestReferenceDatabaseCreate`.

**Docs.** Add the CREATE DATABASE decisions to the ddl README.

## Status

- 166: done (`e560d7aa2`, `3271a36c6`, then the docs commit).
  - On `ddl` (`database.go`): `CreateDatabaseOptions`,
    `ParseCreateDatabaseOptions`, `CreateDatabaseOwner(roles, current, name)`
    and `CheckCreateDatabase(name, options, exists)`. The root keeps
    `databaseCreateOwnerLocked` as the authority capture plus one call (two
    callers), and `executeCreateDatabase` answers `exists` from
    `e.cluster.targets` under `Engine.mu`, then checks the context.
  - Deviations: none. DROP and ALTER DATABASE have not landed, so there was
    nothing more to take. The context check stays in the root.
  - Root 400 files and 49.2k → 49.0k lines (−0.15k); `ddl` gains 0.18k
    production lines.
  - Tests: none moved (no unit test named the moved code). New:
    `TestCheckCreateDatabaseOrder` pins the admission order and the `exists`
    seam.
  - For item 167: `createDatabaseTarget` and `publishDatabaseTargetLocked`
    still reach `Engine` by design (private construction, the owner re-check
    and publication).
