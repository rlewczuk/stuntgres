PL/pgSQL supports unconditional LOOP, labeled EXIT/CONTINUE and WHEN conditions,
including nested block/cursor transfers, cancellation and stored-definition replay.
Quoted label diagnostics preserve literal identifier contents.

Trigger rename, extension dependencies and table-qualified DROP preserve installed
definitions and partition clone identities, participate in transactions, and
survive native Dump/Restore.

Permanent table/schema DDL now participates in SQL and Go transactions, including
private schema/table chains, query-derived tables, savepoints and atomic
definition/data commit or rollback.

Please DO NOT modify this file.

There is no need to record changes anymore.

DO NOT add anything to changelog anywhere in this project.
