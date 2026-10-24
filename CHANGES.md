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
