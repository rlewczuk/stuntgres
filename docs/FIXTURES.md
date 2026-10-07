# Native fixture controls

[README](../README.md) · [SQL dialect and limits](DIALECT.md) · [SQL reference](SQL.md)

```go
func Reset(ctx context.Context, db *sql.DB) error
func Dump(ctx context.Context, db *sql.DB, dst io.Writer) error
func Restore(ctx context.Context, db *sql.DB, src io.Reader) error
```

Controls require a **native stuntgres** pool; foreign and middleware-wrapped pools are rejected, with no reflection unwrapping. They are **host-authorized**, independently of a borrowed connection's effective principal. Reset atomically replaces the catalog lifetime with initialized built-in `pg_catalog` and `public` namespace descriptors while retaining database identity, cluster roles, nonreuse counters, live identities/settings and existing pools. Dump captures before external I/O, groups deterministically by owner name then schema/relation, and writes quoted owner-switch directives plus PostgreSQL-compatible CREATE/INSERT SQL, declaration-ordered columns and canonical literals. Bootstrap-only dumps retain legacy CREATE/INSERT bytes without a redundant switch. Explicit relation/column ACLs cannot be serialized and cause a failure before output, rather than silent loss. Dump excludes role creation, passwords, membership and settings; it is not complete `pg_dump` output, COPY, or catalog export.

Dump reconstructs renamed, absent and recreated bootstrap namespaces using ordinary
DROP/ALTER SCHEMA SQL before user schemas and tables. Retained builtin ACLs must
equal their canonical bootstrap rights with both grantor and owner-grantee
transformed to the current owner; arbitrary explicit ACLs remain unsupported and
are rejected before output. Recreated public is an ordinary namespace with absent
ACL, never silently granted PUBLIC rights. Unchanged bootstrap streams retain
their exact legacy bytes.

Restore accepts supported CREATE/ALTER/DROP SCHEMA, CREATE TABLE with supported
defaults, supported parameter-free scalar/default-request INSERT (including
integer arithmetic), database-local COMMENT and named SET SESSION AUTHORIZATION
owner directives. Parameters are recursively rejected, and other SQL remains
excluded. Legacy CREATE/INSERT-only scripts remain bootstrap-owned. Referenced
roles must already exist in the target engine. Restore captures a private role
snapshot before input I/O, starts an initialized private catalog, executes through
the same SQL engine, remaps owners into target principal authority, then atomically
**replaces** descriptors, storage and epoch, never merges or resets roles/live
sessions. Missing or dropped/recreated owners and read/parse/execution/cancellation
errors leave original data untouched. Empty input clears user relations. No target
locks span external I/O. Private operation-only CREATE destination, INSERT and
database-local COMMENT bootstrap authority preserve independent schema/table owners
without grants or session identity changes. COMMENT's private marker covers lookup,
lock resolution and mutation, is cleared after each statement, and never permits
ROLE replay. Ordinary post-Restore SQL still enforces USAGE/CREATE and ownership.
The shared object allocator skips live namespace and relation identities imported
by Restore, so subsequent schema and table creation continues without collisions.
Replacement does not rewind the destination's nonreuse counter.

`go test -count=1 -run '^TestCatalogOwnersPublicWitness$' ./smoke` additionally checks two opaque DSNs with two simultaneously open pools and held physical connections per instance. It verifies qualified/unqualified INT4 results, same-instance sharing, independent-instance isolation, another instance's Reset/Restore leaving a held cursor unchanged, same-Driver reconnect retention and independent-Driver isolation. Go test reports the result.
