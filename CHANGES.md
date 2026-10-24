Table/type/domain/database OWNER, view/materialized-view OWNER and REASSIGN/DROP
OWNED now share SQL and Go transactions with autocommit and retained protocol
messages. Ownership and ACL/default/policy/membership cleanup see earlier private
changes, restore at savepoints or rollback and publish together at commit.
Sole-target policy deletion retains AccessExclusive on its surviving containing
table until transaction end or rollback to an earlier savepoint, without blanket
partial-policy or ACL-survivor locking.

CREATE, ALTER (including rename) and DROP POLICY now participate in SQL BEGIN
and Go BeginTx as well as autocommit and retained messages. Private definitions,
role bindings, dependencies and rows commit or roll back together, with savepoint
recovery, transaction-lifetime table locking and fresh existing RLS consumers.

BEFORE row trigger CREATE/REPLACE now shares SQL and Go transactions with existing
rename, DROP and extension dependencies, including private firing, savepoint
recovery and atomic definition/row publication. CREATE/REPLACE retains
ShareRowExclusive locks on partition descendants through transaction end.

CREATE/OR REPLACE RULE, ALTER RULE RENAME and DROP RULE now participate in SQL
and Go transactions as well as autocommit. Private definitions govern owner DML
immediately; rule changes, dependencies and action rows commit or roll back
together, including savepoints and held prepared execution.

Ordinary and concurrent REFRESH MATERIALIZED VIEW now participate in SQL BEGIN
and Go BeginTx as well as autocommit. Source writes, stored rows, indexes and
population commit or roll back together, including savepoint recovery, with
transaction-lifetime reader/refresh locking.

Domain and composite creation, domain rule changes, composite attribute evolution
and type/domain rename, schema move and DROP now share SQL and Go transactions
with autocommit and retained messages. Definitions, consuming rows and logical keys
commit or roll back together, with savepoint recovery and exact source authority.

Supported object GRANT/REVOKE and ALTER DEFAULT PRIVILEGES now participate in
SQL and Go transactions and retained messages. Private ACLs and creator defaults
compose with object creation, rows, dependencies and savepoints, and publish
atomically at commit. Named relation grants honor AccessShare admission;
independent ACL/default targets avoid false statement-authority and
role-publication conflicts without changing ordinary lock fast-path allocation.
Completed ACL effects preserve compatible unrelated role publications, while
pending statements retain their separate authority fence. These ACL/default
directives do not widen existing role, ownership or ALTER SYSTEM policies.

View and materialized-view definitions now participate in SQL and Go transactions
as well as autocommit, with private definitions and stored rows, identity-preserving
alterations, savepoints and atomic commit or rollback. REFRESH remains separate.

Ordinary DROP INDEX and ALTER INDEX rename/SET/RESET now participate in SQL,
Go and retained protocol transactions, with private metadata/dependencies,
savepoint recovery and atomic index/row publication. Logical index options
preserve identities and key semantics and survive native Dump/Restore, including
constraint-backed indexes. Completed metadata-only maintenance permits compatible
peer DML and unrelated role changes without retaining stale statement authority
at commit. Pending query-derived producers and materialized replacements retain
their separate role-read publication guard; actual role writes keep their own
baseline conflict fence. REINDEX and concurrent-operation boundaries remain.

Existing role creation, attributes, retirement and membership options now support
SQL and Go transactions, including private visibility, savepoints and atomic
cluster-wide publication. Session authorization rolls back with its physical
owner; role conflicts share the canonical lock and deadlock lifecycle.

PL/pgSQL RAISE supports severity levels, formatted messages, SQLSTATE/condition
forms and structured diagnostic fields. ASSERT evaluates messages only on failure
and honors the session's live plpgsql.check_asserts setting.
Successful nested messages retain their enclosing PL and SQL caller context.

PL/pgSQL static query FOR loops assign existing RECORD and row variables,
preserve query ordering and duplicates, retain VOID query-result members,
shape empty results as NULL fields, stream SELECT demand across CTE lifetimes,
and complete RETURNING effects before loop bodies while retaining statement
atomicity and cancellation cleanup.

PL/pgSQL supports unconditional LOOP, labeled EXIT/CONTINUE and WHEN conditions,
including nested block/cursor transfers, cancellation and stored-definition replay.
Quoted label diagnostics preserve literal identifier contents.

Existing table, column, foreign-table, schema and role comments now participate
in SQL and Go transactions, with private visibility, savepoints and atomic commit
or rollback. Sequence comment and counter behavior is preserved.

`information_schema.tables` exposes privilege-filtered table, view, foreign-table
and session-local temporary identities with typed-table metadata, structural
insertability and current results on held prepared reads.

Trigger rename, extension dependencies and table-qualified DROP preserve installed
definitions and partition clone identities, participate in transactions, and
survive native Dump/Restore.

Ordinary CREATE INDEX, including UNIQUE and shipped access methods/options, now
participates in SQL and Go transactions on existing or transaction-private
tables. Index metadata, keys and rows commit atomically or roll back together;
savepoints restore private work and release postmark locks. Ordinary builds
retain SHARE through transaction end; CONCURRENTLY and standalone maintenance
keep their existing transaction boundaries.

Permanent table/schema DDL now participates in SQL and Go transactions, including
private schema/table chains, query-derived tables, savepoints and atomic
definition/data commit or rollback.

Please DO NOT modify this file.

There is no need to record changes anymore.

DO NOT add anything to changelog anywhere in this project.
