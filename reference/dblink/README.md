# reference/dblink

Public dual-mode scenarios for installed dblink SQL construction, primary-key
metadata and host-bound connection lifecycle. DELETE construction belongs to
[extensions](../extensions/README.md#testing).

## Fixture ownership

The shared family helper owns extension/schema cleanup and pinned sessions.
Constructors and metadata readers open no network connections. Physical
connection phases require `REFERENCE=1` and genuine external PostgreSQL.
The single real pgconn adapter is owned by
[fixture](../fixture/README.md#shared-genuine-external-sql-adapter) and shared with
table-provider scenarios.

Expected SQL and diagnostics are independently specified from released sources.
SQL construction is tested by executing returned TEXT against real local data.
Generated SQL is not a substitute for exercising actual source execution.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` uses `fixture.Main`.
Use `make check RUN='...'`, adding `REFERENCE=1` for the oracle.

- SQL construction and primary-key metadata: `RUN='^TestReferenceDblink(BuildSQL(Insert|Update)|GetPkey)'`
- Connection lifecycle: `RUN='^TestReferenceDblinkConnection'`
