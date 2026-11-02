# internal/engine/rolecmds

The role, privilege and ownership statement executors
([`docs/ENGINE-SPLIT.md`](../../../docs/ENGINE-SPLIT.md) §3, L16): CREATE,
ALTER and DROP ROLE; GRANT and REVOKE of role membership; SET ROLE and SET
SESSION AUTHORIZATION; GRANT and REVOKE on objects and on databases; ALTER
DEFAULT PRIVILEGES; ALTER ... OWNER TO for types, domains, statistics, databases and large objects;
REASSIGN OWNED and DROP OWNED; CREATE DATABASE and ALTER DATABASE defaults; and the `RoleHost` family
host. The decisions are [`role`](../role/README.md)'s, the role graph and
ACL algorithms [`authorization`](../authorization/README.md)'s and the
database option checks [`ddl`](../ddl/README.md)'s; this package
orchestrates them over core's runtime.

## Layering

It imports [`core`](../core/README.md) and the decision packages, never the
root or another family; `rolecmds` stands alone (rule 6). In production only
the root composition imports it.

Stays in core: authorization capture and `AuthorizationView`, the role
locks on the statement path (`StatementScope.AcquireRolePrincipal`,
`AcquireRoleResource`, `AcquireRoleDependencies`, `RoleStillPresent`),
publication admission (`PublicationOperationLocked`), the role installation
the schema statements share (`InstallOperationRolesLocked`), relation
ownership (`ExecuteRelationOwnership`, `OwnershipStage`,
`PublishOwnership`), the role store, the cluster registry and the engine
factory hook (`Engine.Factory`).

## Entry points

Dispatch (`core.Statements.Execute`); each is entered with nothing held:
- `ExecuteRoleAttributes` (CREATE and ALTER ROLE) validates under
  `Engine.Mu`, takes the role name or principal locks unlocked, prepares the
  password and VALID UNTIL inputs with no lifecycle lock held, then
  revalidates and reads typed `createrole_self_grant` from EffectiveSettings.
  Descriptor, bootstrap ADMIN and optional creator-origin INHERIT/SET grants
  share one candidate, initial-membership locks and transactional publication
  fence. A changed record or allocator restarts the decision with current options.
- `ExecuteRoleRename` revalidates current authority after cluster principal and
  destination-name waits, preserves the principal and complete defaults, and
  replaces the immutable descriptor/name index. Only MD5 verifiers are cleared,
  with one notice after successful statement installation.
- `ExecuteRoleSettings` and `ExecuteDatabaseSettings` adapt syntax to one row
  executor: resolve current authority, wait on the exact cluster scope, then
  reauthorize and validate through the existing setting/parameter-ACL owners.
  ALTER DATABASE uses inherited database ownership and the same database-only
  row as ALTER ROLE ALL IN DATABASE. Owner-dependent writes journal the database
  predecessor before installation, so later owner changes veto commit. Protected
  RESET ALL entries remain. The normal role journal, savepoints and Finish
  publication apply without changing issuer overrides or reset images.
- `ExecuteRole` (role membership GRANT and REVOKE, SET ROLE, SET SESSION
  AUTHORIZATION, DROP ROLE) decides under `Engine.Mu`. Membership releases it
  for its locks and rechecks the role record; SET ROLE and SESSION
  AUTHORIZATION atomically journal/install the complete current-ref/active/text
  role value and session identity under `Settings.Mu` and then `Session.Mu`,
  after publication and revision preflights. Explicit authorization clears the
  active role; RESET/DEFAULT restores its captured startup ref/text pair without
  rechecking membership. `AcquireRoleRetirementLocks` runs before DROP ROLE.
  The same decided state supplies core's original/prospective statistics
  admission before mutation; normalization does not resolve the target again.
  Failed decisions admit only the original principal and publish no success.
- `ExecuteObjectGrant`, `ExecuteDatabaseGrant` and
  `ExecuteDefaultPrivileges` (GRANT and REVOKE on objects and databases,
  ALTER DEFAULT PRIVILEGES) plan under `Engine.Mu`. Object grants admit
  their named relations with AccessShare and default privileges their
  default-key writers, unlocked, then recheck the role record and targets.
  Each leaves its candidate in the statement scope; `Finish` keeps it
  private or publishes it with the transaction's other work.
- `ExecuteOwnership` (ALTER ... OWNER TO) stages type, domain, database and
  large-object owners and hands relation kinds to `core.ExecuteRelationOwnership`.
  Statistics resolve the recipient first, then retain the original definition and
  live recipient through unlocked AccessExclusive definition/dependency admission.
  Current authority is recaptured under Mu after waiting; source/name/namespace,
  epoch and recipient-generation changes cannot redirect a pending command.
  Same-owner success is resolved and locked before returning without a revision.
  Large-object transfer reuses the ownership publication/Finish journal and
  validates catalog/role/session predecessors without a relation lock or byte copy;
  `ExecuteOwned` (REASSIGN and DROP OWNED) pins every ownership root and
  policy survivor in one canonical order before staging and publishes one
  ownership stage, with DROP OWNED's cascade notice after it. After language or
  shared subscription object admission, it recaptures current authorization and
  rebuilds the still-empty stage, retaining the admitted role/root identities.
  Language roots additionally retain their exact descriptor predecessors.
  Subscription peers' committed fields survive REASSIGN without a stale candidate,
  command retry or suppressed publication conflict. Ordinary publication acquires
  the shared definition-dependency, role, retirement and global-name admissions.
- `ExecuteCreateDatabase` admits the exact registered template source, privately
  copies its committed catalog/storage, rechecks source and owner authority and
  registers the completed target in one publication. An unregistered `template1`
  alone uses the cluster's lazy private builtin seed.

`RoleHost{core.DDLHost}` (`role_host.go`) implements `role.Host` and
`role.StageHost`; only these executors build it. The decisions made outside a
stage get a host with no stage.

## Database creation

The create executor accepts OWNER, registered TEMPLATE, default/UTF8 ENCODING
(including numeric 6), C/POSIX locale options, IS_TEMPLATE, ALLOW_CONNECTIONS,
CONNECTION LIMIT and validated WAL_LOG/FILE_COPY strategies. Either strategy
copies the complete logical contents; physical WAL, checkpoint and filesystem
effects belong outside the logical runtime. Template0 bootstrap, other locale
providers and physical placement are not supplied by this path.

CREATEDB is the effective role's direct attribute (or superuser), with SET ROLE
authority required for the new owner. Copying a non-template additionally requires
the executor's inherited source ownership, independently of the requested owner
and CONNECT grants. Source allow-connections policy does not restrict copying.
Physical reservations and detached prepared transactions make the source busy;
only the actual executing physical source owner is excluded. Compatible copies
share a source lease, blocking new physical admissions until the last release.
Source replacement read protection precedes shared Mu then AdmissionMu; no
metadata lock crosses preparation or waits.

Copied objects retain local SQL OIDs, dependencies, owners and ACLs in a fresh
catalog/storage authority. Transaction-private state and live source TEMP
attachment/contents are excluded. TEMP catalog metadata is imported through the
same graph remapper with opaque destination-detached ownership and private empty
backing; it does not initialize a destination session's TEMP namespace.
Qualified access to this orphan TEMP metadata is not a physical-file parity
promise: PostgreSQL can report `58P01` for a missing copied TEMP relation file,
whereas the logical runtime rejects other-session TEMP access with `0A000`.
Both retain the shared nonattachment and source/target isolation guarantees;
the embedded runtime does not fabricate PostgreSQL filenames or file errors.
Mutable rows, indexes and sequence generators are independent. Target database
ACL/configuration is not imported; template/allow/limit defaults are false/true/−1.
Registered template1 additions win over the private fallback seed. Prepare is
syntax-only; execution validates values, owner, source permission, options and
duplicate name before busy admission. Final operation, source and owner fences
precede indivisible registration. Failure/cancellation publishes nothing and
unconditionally releases source exclusion. This path does not mutate database
policy through ALTER DATABASE.

## Testing

Shared parameter final-registration ownership uses
`make check RUN='^TestParameterACLShared'` (also with `RACE=1`), including the
genuine detached template runtime and an intervening committed grant.

Role creation self-grant SQL coverage lives in `reference/object_address`:
`make check RUN='^TestReferenceCreateroleSelfGrant'`, also with `REFERENCE=1`.

Tests are external (`package rolecmds_test`) and run on a composed engine:
the root's `engine.New`, `Reset`, `Dump` and `Restore` and the
[`enginetest`](../enginetest/README.md) helpers. `export_test.go` exports the
internals they call; add a name there only when the test cannot assert
through SQL or the exported API. The package's `testSessionID` starts above
the session IDs `enginetest` mints, so a test may use both. Public SQL lives
in [reference/roles](../../../reference/roles/README.md#testing),
[reference/object_grants](../../../reference/object_grants/README.md#testing)
and, for CREATE DATABASE,
[reference/catalog_privileges](../../../reference/catalog_privileges/README.md#testing).

```sh
make check RUN='^Test(Role|Membership|Predefined|SessionIdentity|DriverBatchIdentity|AlterNullabilityAuthorization|Authorization(Public|Namespace))'  # roles, membership, SET ROLE, identity
make check RUN='^Test(ObjectGrant|ObjectRevoke|DefaultPrivileges|TransactionalPrivileges|Database)'  # grants, default ACLs and database command ownership
make check RUN='^Test(RoleOwnership|ReassignOwned|DropOwned|LargeObjectOwner)'  # ALTER OWNER, REASSIGN and DROP OWNED
make check RACE=1 RUN='^Test(RoleOwnershipPublication|ReassignOwnedPublication|RoleTransactional|RoleSwitch|DatabaseCreate)'  # publication and session races
```
