# PostgreSQL 18.0 regtype oracle facts

## Authority and inventory

The root's initial pinned run is recorded in
`.cswd/parts/PG18-TYPE-REG-TYPE/tests-1.md`: PostgreSQL 18.0, UTF8/C, the fixture's
unchanged `postgres:18.0-bookworm` image. Its exit 2 is discovery evidence, not
passing acceptance. No production implementation or native missing-behavior
proof has run at this stage.

[regtype-pg18-inventory.tsv](regtype-pg18-inventory.tsv) releases all **621**
pristine `pg_catalog` and `information_schema` identities from that report.
It retains namespace/name, type OID/kind, raw element, associated array, domain
base/typmod, modifier input/output providers, owning relation, storage,
subscript handler, namespace OID and default-path symbolic output. This is
compact checked-in research data for production handoff, not an executable
parallel name registry or a requirement to expose native pg_type SQL.

**198 type OIDs are fixed source identities; 423 are allocated observations.**
Each row labels its type, owning-relation and namespace provenance separately.
The fixed types come from release pg_type.dat/generated arrays and the fixed
header rowtypes below; the allocated type IDs in this particular initialized
image start above 10000. That threshold describes this captured dataset only,
not a general allocator/classification rule.

- Release base/array declarations:
  [pg_type.dat](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/pg_type.dat),
  [Catalog.pm](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/Catalog.pm).
- Fixed header rowtypes: `pg_database`1248, `pg_authid`2842,
  `pg_auth_members`2843, `pg_shseclabel`4066, `pg_subscription`6101.
  Their separately allocated arrays are not fixed source OIDs. Four bootstrap
  rowtypes `pg_type`71, `pg_attribute`75, `pg_proc`81 and `pg_class`83 and their
  arrays are already in pg_type.dat.
- Catalog owning relation OIDs come from
  [all CATALOG_HEADERS](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/include/catalog/Makefile).
  They are not their rowtype OIDs: pg_database relation1262 versus type1248.
- System-view/table/domain names come from all declarations in
  [system_views.sql](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/system_views.sql)
  and [information_schema.sql](https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/catalog/information_schema.sql).
  Their type/array and relation OIDs are initdb allocations.
- `pg_catalog` namespace11 is fixed; observed `information_schema` namespace13229
  is allocated. Neither allocated namespace nor type numbers are portable
  cross-backend expectations.

Use actual reciprocal array links, never arithmetic guesses. `record`2249 has
associated `_record`2287, both pseudo kind `p`. `void`/`unknown` have no arrays.
Raw typelem does not classify true arrays: scalar `name`/geometry and plain
vectors retain their scalar formatting. Storage/subscript plus actual array
association distinguish them. Delimiters were not projected by this run;
release pg_type.dat/pg_type.h establish `box` and its array delimiter `;`,
otherwise `,`. Do not claim those delimiter fields were live observations.

The external reference expectation table is independent from production data.
All 621 identities now have name-to-identity and numeric-to-symbolic SQL
witnesses; source-fixed OIDs are asserted literally. Allocated OIDs are captured
inside each backend then compared with fresh symbolic lookup in that same
backend. Full source enumeration lists and their array-identity comparisons
remain; no pg_type native projection or cross-backend dynamic OID parity is
substituted for regtype behavior.

## Grounded input, diagnostics and transport

The permanent diagnostic matrix encodes all 43 original inputs through literal
Query, literal Prepare, direct frame, same-held frame, runtime TEXT and
same-held runtime TEXT. Every structured record includes severity, state,
primary, Detail/Hint, outer/internal locations, internal query, Context and
logical object fields, including exact empty fields. Shared fixture assertions
exclude fabricated native PostgreSQL C source locations.

Key corrections and observations:

- `integer..x`:42601 token `..`, not `.`; malformed grammar carries original
  `invalid type name` Context. Literal UTF8-prefix location17 replaces inner
  location; frame/runtime preserve decoded-input character locations.
- `interval(-1)`:42601 syntax at `-`, frame/runtime position10, not a typmod
  bounds22023 result. `float(0/54)` bounds errors retain parser Context and
  decoded position7. Provider errors such as int4(4) do not get parser Context.
- Empty input has portal `= ''`; other direct/held frames use `= '...'`.
  Runtime TEXT input has no portal Context. Second frame slot attributes `$2`.
- SQL BOOLEAN cast position12; known TEXT assignment position47 plus exact
  rewrite/cast Hint. Negative BIGINT conversion fails at execution after
  successful Prepare. Literal multirow INSERT/UPDATE input fails during Prepare.
- REGTYPE/_REGTYPE pq raw values are nonnil `[]byte`, ScanType `interface{}`,
  with no length/decimal/nullable metadata. SQL NULL is nil. Native expectations
  remain string/string with declared known nullability versus computed unknown.
- Domain over regtype reports REGTYPE; domain over regtype[] reports _REGTYPE.
  Array of user domain has empty pq DatabaseTypeName; native `_IDENTIFIER` in
  this fixture. Its pq bytes/interface{} transport is asserted independently.
- Identifier clipping has four complete NOTICE42622 records: two ASCII and two
  UTF8 input executions, with invalid-type-name plus parameter Context.

## Held literals and warning delivery

The same held literal handle initially refers to A.same; changing search_path
reparses it to B.same, restoring the path reparses it to A.same. Dropping A.same
alone leaves the held numeric identity unchanged and its output becomes that
old OID's decimal text. Recreating A.same does not retarget that held constant;
fresh symbolic lookup identifies a different type. The permanent shared test
uses backend-local A/B identities, never snapshot allocation numbers.

`timestamp(7)` literal Prepare emits exactly one WARNING22023 at outer position8:
`TIMESTAMP(7) precision reduced to maximum allowed, 6`. Both held executions
emit none. Parameter/runtime Prepare emits none; each held parameter execution
emits exactly one warning. Direct regtype frames include parameter Context,
runtime TEXT does not. All remaining warning fields are asserted empty.

Schema rename leaves the retained cursor's old `regtype_a.same` value unchanged
on PostgreSQL as well as native; fresh execution outputs `regtype_renamed.same`
and preserves numeric identity. Stored dependency RESTRICT diagnostics and the
six-object CASCADE notice Detail/Hint/order are encoded. Surviving tables and
scalar columns are witnessed with ordinary SQL, replacing catalog logging.

## Outstanding oracle observations and transition

The bad original varchar alias query stopped the input scenario before later
conversion/IO assertions. Public schema USAGE under SET ROLE prevented the
intended denied-table/input ordering witness. The reachable corrections are:

1. Use `pg_catalog.varchar('12')` and `pg_catalog.varchar((12))` for valid generic
   simple-constant modifiers; retain alias `varchar('12')` and `varchar((12))`
   as independent six-route grammar-rejection discovery cases.
2. Create a reader-owned accessible schema, then a superuser-owned denied table
   there. Exercise bad/valid input on direct and same-held INSERT, require nil
   results and zero mutation. Namespace authorization no longer masks table
   permission ordering.

Only Input, Authorization and PendingSyntax retain temporary discovery filters
and deliberate observation failures. Root must capture, with a 900-second
deadline and assigned worktree cwd:

```sh
make check REFERENCE=1 RUN='^TestReferenceRegtype(Input|Authorization|PendingSyntax)$'
```

Expected discovery result: nonzero solely from explicit remaining-observation
failures if independent assertions are correct; complete new error records are
visible. Generic qualified varchar must return character varying; invalid alias
modifiers42601; missing input42704 must precede valid-input table42501. These
new observations are not claimed exercised yet.

After encoding those records and removing the narrow filter/log/failure helper,
root runs the native missing-behavior proof. Production still waits that proof.
Both completed selectors and feature smokes are required before review, each
quickcheck under a 900-second deadline:

```sh
make check RUN='^TestReferenceRegtype'
make check REFERENCE=1 RUN='^TestReferenceRegtype'
```

Native proof initially must fail with actual absent regtype support. After full
production implementation all completed selective checks/smokes must exit0.
Native Dump/Reset/Restore and actual emitted PostgreSQL replay remain in the
shared tests, but no emitted native stream has been observed at this stage.
