# reference/alter

Dual-mode scenarios for ALTER TABLE and transactional schema lifecycle.
Standalone table definitions belong to [tables](../tables/README.md#testing);
identity/default/generated columns to [sequences](../sequences/README.md#testing);
wire routes to [sessions](../sessions/README.md#testing).
Physical PostgreSQL pages and WAL/crash durability are outside the logical API contract.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses default options.
Family-local scenario, catalog snapshot and control helpers remain in
`*_helpers_test.go`.

- Each serial route owns its scenario and schema/role cleanup. Workers register
  cancellation and bounded joins before handles close.
- `*SourceConsumerRoutes` exercises plain `database/sql` methods using the same
  corpus as its Context companion; keep both routes together.
- PostgreSQL tablespaces need real container-owned directories: register drop
  and directory cleanup before creating them.
- Database-global access methods require cleanup independently of schema reset.
- PostgreSQL replay ignores native materialized-view snapshot comments. Assert
  unpopulated state before explicit REFRESH; native Restore retains captured
  rows and population flags. Cleanup runs after failed replay too.
- Maintenance SQL results do not prove physical reorganization or index traversal.
  Keep logical state/replay assertions separate from physical-engine witnesses.

Run a family with `make check RUN='...'`; add `REFERENCE=1` for the oracle.

- ALTER actions: `RUN='^TestReferenceAlter'`
- Transactional schemas: `RUN='^TestReferenceTransactionalSchema'`
- Constraint modes/enforcement: `RUN='^TestReferenceConstraint'`
- Clustering: `RUN='^TestReferenceCluster'`
- The `unreadview` child package owns frozen unread-view source-layout scenarios:
  `RUN='^TestReferenceAlterTypeUnreadView'`.
