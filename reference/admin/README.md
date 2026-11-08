# reference/admin

Dual-mode reference coverage for the administrative `COMMENT` utility over the
object kinds PostgreSQL 18 documents beyond the long-covered relation, column,
schema, role, sequence and extension targets: `TYPE` (enum, standalone
composite, range, table row type, array and builtin), `DOMAIN`, `INDEX`,
`TRIGGER` and `FUNCTION`/`PROCEDURE`/`ROUTINE` with and without an argument
list.

Each comment is stored on the existing metadata of its object and is readable
through the same surfaces PostgreSQL uses:

- `pg_catalog.pg_description` row `(objoid, classoid, objsubid=0)` with
  `classoid` **1247** (`pg_type`) for types and domains, **1255** (`pg_proc`)
  for routines, **1259** (`pg_class`) for indexes (never `pg_index`) and
  **2620** (`pg_trigger`) for triggers;
- `obj_description(oid, catalog)` for every kind and the legacy one-argument
  `obj_description(oid)` form;
- `col_description` for column comments, unchanged;
- `NULL` and `''` remove the row, a single space persists, and re-issuing
  `IS NULL` on an uncommented object succeeds.

`COMMENT` stays a transactional utility: inside `BEGIN`/`ROLLBACK` the
previous text survives, and inside `BEGIN`/`COMMIT` it is published. Every
statement reports zero affected rows and no result columns on both execution
routes.

Two negative facts are part of the public contract rather than gaps:

- PostgreSQL 18's `information_schema` has no object-comment column (its
  `information_schema.sql` never reads `pg_description`). The record's
  "information_schema" requirement is therefore satisfied by asserting that the
  commented objects themselves are visible in `information_schema.domains`,
  `.routines`, `.triggers` and `.columns`; no comment column or non-PostgreSQL
  surface is invented. `TYPE` and `INDEX` have no `information_schema` view at
  all and are covered by `pg_description`/`obj_description` only.
- `pg_catalog.pg_shdescription` only holds database, tablespace and role
  comments. None of the classoids above gains a row there.

Native `Dump`/`Reset`/`Restore` emission for these kinds is deliberately out of
the record's scope: the tests add no dump assertions and expect no new
serialized `COMMENT` forms. Restore replay of the new kinds stays rejected by
the unchanged trusted-Restore allowlist.

## Testing

Follow [docs/TESTING.md](../../docs/TESTING.md). `TestMain` is plain
`fixture.Main(m)`; the pinned PostgreSQL 18.6 oracle runs only under
`REFERENCE=1`.

- One scenario per execution route (`direct`, `prepared`) and one body over
  `p.Backends()`, so public object names stay literal and every statement is
  the application's spelling. `comment_object_kinds_helpers_test.go` holds the
  route/session harness, the `pg_description` address probes and the scalar
  identity helper. Utility steps are prepared on the prepared route; catalog
  probes stay direct observations.
- `TestReferenceAdminCommentObjectKinds` runs the exact round-2 repro, the
  kind/readback matrix, removal and replacement, transactionality, DROP
  semantics (including a re-created trigger getting a new oid and no inherited
  text) and the `information_schema` visibility witnesses.
- `TestReferenceAdminCommentObjectKindsDiagnostics` pins the exact runtime
  SQLSTATEs/messages for missing, wrong-kind, USAGE-denied, ambiguous and
  syntax targets, plus the owner-only `42501` texts for every new kind and the
  superuser override. Runtime diagnostics carry no `Position`; the single
  syntax case reports one (the prepared route observes it at prepare time).
- `TestReferenceAdminCommentObjectKindsFeatureSmoke` replays the exact round-2
  repro and every `COMMENT` statement occurrence of the 25 named coder
  migrations byte-for-byte (`*.up.sql` then `*.down.sql`, duplicates included)
  on a minimal synthetic schema and asserts the exact final literals.

Add `REFERENCE=1` for the oracle.

- Kinds, readback and lifecycle: `RUN='^TestReferenceAdminCommentObjectKinds$'`
- Diagnostics and authorization: `RUN='^TestReferenceAdminCommentObjectKindsDiagnostics$'`
- Coder migration COMMENT stream: `RUN='^TestReferenceAdminCommentObjectKindsFeatureSmoke$'`
- All families: `RUN='^TestReferenceAdminCommentObjectKinds'`